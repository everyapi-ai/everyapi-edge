package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	edgeapp "github.com/everyapi-ai/everyapi-edge/internal/app"
	"github.com/everyapi-ai/everyapi-edge/internal/client"
	"github.com/everyapi-ai/everyapi-edge/internal/config"
	"github.com/everyapi-ai/everyapi-edge/internal/console"
	"github.com/everyapi-ai/everyapi-edge/internal/forward"
	"github.com/everyapi-ai/everyapi-edge/internal/identity"
	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
	edgeupdate "github.com/everyapi-ai/everyapi-edge/internal/update"
)

func runGatewayLifecycle(
	ctx context.Context,
	cfg config.Config,
	id identity.Decoded,
	meta protocol.NodeMeta,
	fwd *forward.Forwarder,
	updateManager *edgeupdate.Manager,
	controlHandler http.Handler,
	store *console.Store,
	logSink *logTee,
) error {
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
		sessionMeta := meta
		models, modelErr := discoverOllamaModels(ctx, cfg.OllamaURL)
		if modelErr != nil {
			log.Printf("warning: could not discover Ollama models: %v", modelErr)
		} else {
			sessionMeta.Models = mergeModels(sessionMeta.Models, models)
			log.Printf("discovered %d Ollama models", len(models))
		}
		if cfg.DiffusersURL != "" {
			imageModels, imageModelErr := discoverDiffusersModels(ctx, cfg.DiffusersURL)
			if imageModelErr != nil {
				log.Printf("warning: could not discover Diffusers models: %v", imageModelErr)
			} else {
				sessionMeta.Models = mergeModels(sessionMeta.Models, imageModels)
				log.Printf("discovered %d Diffusers models", len(imageModels))
			}
		}

		cli, err := client.New(client.Config{
			GatewayURL:        cfg.GatewayURL,
			OllamaURL:         cfg.OllamaURL,
			VRAMTotalGB:       sessionMeta.Hardware.VRAMTotalGB,
			NodeID:            cfg.NodeID,
			RegistrationToken: registrationToken,
			Identity:          id,
			Meta:              sessionMeta,
			OnConnected: func() {
				if updateManager != nil {
					if err := updateManager.Promote(); err != nil {
						log.Printf("warning: could not promote successful update: %v", err)
					}
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
