package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Auto-pull: the gateway's Welcome frame carries the models this node is
// missing relative to the target set its owner declared in the dashboard
// (WelcomeBody.RecommendedModels). The agent pulls them itself, so a
// supplier running many machines declares the set once instead of SSHing
// into every box to run `ollama pull`.
//
// Deliberately separate from the Control Room's pull path in
// internal/console. That one streams progress into a job the local UI
// polls; this one is a background task with nobody watching, so it wants
// the opposite trade-offs: no shared state, no job registry, and failures
// that are logged rather than surfaced. The overlap is the HTTP call
// itself, which is short enough that coupling the two would cost more in
// entanglement than it saves in lines.

// autoPullTimeout bounds a single model download. Large models over a slow
// home connection genuinely take a long time, so this is generous — it
// exists to stop a wedged request from parking the goroutine forever, not
// to enforce a service level.
const autoPullTimeout = 2 * time.Hour

// maxAutoPullModels caps how many models one Welcome frame can ask for.
// The gateway already bounds the declared target set, but the agent must
// not trust a compromised or buggy gateway to keep it bounded — this runs
// on someone else's hardware and fills their disk.
const maxAutoPullModels = 20

// pullRecommendedModels downloads each model the gateway asked for, one at
// a time. Sequential on purpose: parallel pulls of multi-gigabyte weights
// contend for the same disk and network, and ollama serialises them
// internally anyway.
//
// Newly pulled models reach the platform on the next reconnect, when the
// agent re-enumerates its installed set into the Auth frame. That is the
// existing "I added a model" path — no extra reporting frame is needed.
//
// Errors are logged and never propagate: a failed pull must not take down
// a session that is otherwise serving traffic fine with the models the
// node already has.
func (c *Client) pullRecommendedModels(ctx context.Context, models []string) {
	if c.cfg.OllamaURL == "" {
		return
	}
	if len(models) > maxAutoPullModels {
		c.log("info", fmt.Sprintf("auto-pull: gateway asked for %d models, capping at %d", len(models), maxAutoPullModels))
		models = models[:maxAutoPullModels]
	}
	for _, model := range models {
		if ctx.Err() != nil {
			return
		}
		c.log("info", fmt.Sprintf("auto-pull: downloading %s", model))
		if err := pullOllamaModel(ctx, c.cfg.OllamaURL, model); err != nil {
			c.log("info", fmt.Sprintf("auto-pull: %s failed: %v", model, err))
			continue
		}
		c.log("info", fmt.Sprintf("auto-pull: %s ready", model))
	}
}

// pullOllamaModel runs one blocking `ollama pull`. The streaming response
// is drained rather than parsed: only completion matters here, and ollama
// signals failure through the HTTP status plus a truncated stream.
func pullOllamaModel(ctx context.Context, ollamaURL, model string) error {
	payload, err := json.Marshal(struct {
		Name   string `json:"name"`
		Stream bool   `json:"stream"`
	}{Name: model, Stream: true})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, autoPullTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		ollamaURL+"/api/pull", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama returned %s", response.Status)
	}
	// Drain the progress stream to completion. Bounded per line so a
	// misbehaving upstream can't grow the buffer without limit; the whole
	// body is unbounded by design because a pull emits progress for as
	// long as the download runs.
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}
