package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	edgeapp "github.com/everyapi-ai/everyapi-edge/internal/app"
	"github.com/everyapi-ai/everyapi-edge/internal/client"
	"github.com/everyapi-ai/everyapi-edge/internal/config"
	"github.com/everyapi-ai/everyapi-edge/internal/console"
	"github.com/everyapi-ai/everyapi-edge/internal/forward"
	"github.com/everyapi-ai/everyapi-edge/internal/identity"
	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
	edgeruntime "github.com/everyapi-ai/everyapi-edge/internal/runtime"
	edgeupdate "github.com/everyapi-ai/everyapi-edge/internal/update"
)

type metadataRefresh struct {
	generation atomic.Uint64
	changes    chan struct{}
}

func newMetadataRefresh() *metadataRefresh {
	return &metadataRefresh{changes: make(chan struct{}, 1)}
}

func (r *metadataRefresh) Notify() {
	if r == nil {
		return
	}
	r.generation.Add(1)
	select {
	case r.changes <- struct{}{}:
	default:
	}
}

func (r *metadataRefresh) beginDiscovery() uint64 {
	generation := r.generation.Load()
	select {
	case <-r.changes:
	default:
	}
	return generation
}

func rediscoverUntilStable(r *metadataRefresh, discover func() protocol.NodeMeta) protocol.NodeMeta {
	if r == nil {
		return discover()
	}
	for {
		generation := r.beginDiscovery()
		meta := discover()
		if r.generation.Load() == generation {
			return meta
		}
		log.Print("local runtime metadata changed during discovery; rebuilding Auth snapshot")
	}
}

func runGatewayLifecycle(
	ctx context.Context,
	cfg config.Config,
	id identity.Decoded,
	meta protocol.NodeMeta,
	fwd *forward.Forwarder,
	updateManager *edgeupdate.Manager,
	controlHandler http.Handler,
	updateStatus func(edgeupdate.Status),
	store *console.Store,
	logSink *logTee,
	metadataRefresh *metadataRefresh,
) error {
	if metadataRefresh == nil {
		metadataRefresh = newMetadataRefresh()
	}
	var startAutoUpdates sync.Once
	reconnector := edgeapp.Reconnector{
		Hooks: edgeapp.Hooks{
			Connecting: func() { store.SetGatewayState("connecting", "") },
			Active: func(session edgeapp.Session) {
				if active, ok := session.(*client.Client); ok {
					logSink.client.Store(active)
				}
			},
			Inactive: func() { logSink.client.Store(nil) },
			Offline:  func(err error) { store.SetGatewayState("offline", err.Error()) },
			Reconnect: func(next time.Time, attempt int) {
				store.ScheduleGatewayReconnect(next, attempt)
				log.Printf("gateway session ended; reconnect attempt %d scheduled at %s", attempt, next.Format(time.RFC3339))
			},
		},
		NextBackoff: nextBackoff,
		IsTerminal: func(err error) bool {
			var terminal *client.TerminalDisconnectError
			return errors.As(err, &terminal)
		},
		OnTerminal: func(err error) error {
			var terminal *client.TerminalDisconnectError
			if !errors.As(err, &terminal) {
				return err
			}
			log.Printf("terminal disconnect from gateway: %s (%s) — agent will not retry", terminal.Code, terminal.Reason)
			if writeErr := writeRevokedSentinel(cfg.IdentityPath, terminal.Reason); writeErr != nil {
				log.Printf("warning: failed to persist revoked sentinel: %v", writeErr)
			}
			return nil
		},
	}

	return reconnector.Run(ctx, cfg.RegistrationToken, func(ctx context.Context, registrationToken string) (edgeapp.Session, error) {
		var textModels []string
		var textResponsesSupported bool
		sessionMeta := rediscoverUntilStable(metadataRefresh, func() protocol.NodeMeta {
			sessionMeta := meta
			models, modelErr := discoverOllamaModels(ctx, cfg.OllamaURL)
			if modelErr != nil {
				textModels = nil
				log.Printf("warning: could not discover Ollama models: %v", modelErr)
			} else {
				textModels = append([]string{}, models...)
				sessionMeta.Models = mergeModels(sessionMeta.Models, models)
				textCapabilities, responsesSupported, capabilityErr := discoverTextRuntimeState(ctx, cfg.OllamaURL, models)
				textResponsesSupported = responsesSupported
				sessionMeta.Capabilities = append(sessionMeta.Capabilities, textCapabilities...)
				if capabilityErr != nil {
					log.Printf("warning: some Ollama model capabilities could not be discovered: %v", capabilityErr)
				}
				log.Printf("discovered %d Ollama models", len(models))
			}
			if cfg.DiffusersURL != "" {
				health, healthErr := edgeruntime.NewImageClient(cfg.DiffusersURL, &http.Client{Timeout: 10 * time.Second}).Health(ctx)
				if healthErr != nil {
					log.Printf("warning: could not discover image runtime capabilities: %v", healthErr)
				} else {
					sessionMeta.Capabilities = append(sessionMeta.Capabilities, protocolCapabilities(protocol.RuntimeImage, health)...)
					sessionMeta.Models = mergeModels(sessionMeta.Models, readyRuntimeModels(health))
					log.Printf("image runtime is %s with %d models", health.Status, len(health.Models))
				}
			}
			if cfg.SpeechURL != "" {
				health, healthErr := edgeruntime.NewSpeechClient(cfg.SpeechURL, &http.Client{Timeout: 10 * time.Second}).Health(ctx)
				if healthErr != nil {
					log.Printf("warning: could not discover speech runtime capabilities: %v", healthErr)
				} else {
					sessionMeta.Capabilities = append(sessionMeta.Capabilities, protocolCapabilities(protocol.RuntimeSpeech, health)...)
					sessionMeta.Models = mergeModels(sessionMeta.Models, readyRuntimeModels(health))
					log.Printf("speech runtime is %s with %d models", health.Status, len(health.Models))
				}
			}
			return sessionMeta
		})

		cli, err := client.New(client.Config{
			GatewayURL:             cfg.GatewayURL,
			OllamaURL:              cfg.OllamaURL,
			DiffusersURL:           cfg.DiffusersURL,
			SpeechURL:              cfg.SpeechURL,
			VRAMTotalGB:            sessionMeta.Hardware.VRAMTotalGB,
			GatewayRoundTrip:       store.SetGatewayRoundTrip,
			MetadataChanged:        metadataRefresh.changes,
			NodeID:                 cfg.NodeID,
			RegistrationToken:      registrationToken,
			Identity:               id,
			Meta:                   sessionMeta,
			TextModels:             textModels,
			TextResponsesSupported: textResponsesSupported,
			OnConnected: func() {
				if updateManager != nil {
					if err := updateManager.Promote(); err != nil {
						log.Printf("warning: could not promote successful update: %v", err)
					}
					startAutoUpdates.Do(func() {
						go updateManager.RunAuto(ctx, updateStatus)
					})
				}
				store.SetGatewayState("online", "")
				log.Printf("connected to gateway with %d models", len(sessionMeta.Models))
			},
			Handler:        fwd.Handle,
			ControlHandler: remoteControlHandler(controlHandler),
			Log: func(level, message string) {
				log.Printf("[%s] %s", level, message)
			},
			Settlement: func(receipt protocol.SettlementBody) {
				store.Settle(console.Settlement{
					RequestID:          receipt.RequestID,
					SellerAmountMicros: receipt.SellerAmountMicros,
					SettledAt:          time.UnixMilli(receipt.SettledAtUnixMs).UTC(),
				})
			},
		})
		if err != nil {
			return nil, fmt.Errorf("client.New: %w", err)
		}
		log.Print("connecting")
		return cli, nil
	})
}
