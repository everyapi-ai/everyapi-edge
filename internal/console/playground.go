package console

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func (h *handler) playgroundChat(w http.ResponseWriter, r *http.Request) {
	var input playgroundInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 6<<20)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("a valid model and chat messages are required"))
		return
	}
	input.Model = strings.TrimSpace(input.Model)
	if !validModelName.MatchString(input.Model) || len(input.Messages) == 0 || len(input.Messages) > 40 {
		writeError(w, http.StatusBadRequest, errors.New("a valid model and chat messages are required"))
		return
	}
	for _, message := range input.Messages {
		if (message.Role != "system" && message.Role != "user" && message.Role != "assistant") || (strings.TrimSpace(message.Content) == "" && len(message.Images) == 0) || len(message.Content) > 32<<10 || len(message.Images) > 4 {
			writeError(w, http.StatusBadRequest, errors.New("each chat message needs a role and non-empty content"))
			return
		}
		if len(message.Images) > 0 && message.Role != "user" {
			writeError(w, http.StatusBadRequest, errors.New("only user messages can include images"))
			return
		}
		for _, image := range message.Images {
			decoded, err := base64.StdEncoding.DecodeString(image)
			if err != nil || len(decoded) == 0 || len(decoded) > 4<<20 {
				writeError(w, http.StatusBadRequest, errors.New("each image must be a valid file up to 4 MB"))
				return
			}
		}
	}
	input.System = strings.TrimSpace(input.System)
	if len(input.System) > 8<<10 {
		writeError(w, http.StatusBadRequest, errors.New("system prompt is too long"))
		return
	}
	if input.Temperature != nil && (*input.Temperature < 0 || *input.Temperature > 2) {
		writeError(w, http.StatusBadRequest, errors.New("temperature must be between 0 and 2"))
		return
	}
	if input.System != "" {
		input.Messages = append([]PlaygroundMessage{{Role: "system", Content: input.System}}, input.Messages...)
	}
	canLoad, err := h.canStartPlaygroundModel(r.Context(), input.Model)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if !canLoad {
		writeError(w, http.StatusConflict, errors.New("not enough available local memory to load this model; unload another model first"))
		return
	}
	startedAt := time.Now().UTC()
	containsImages := false
	for _, message := range input.Messages {
		containsImages = containsImages || len(message.Images) > 0
	}
	playgroundPath := "/v1/chat/completions"
	if containsImages {
		playgroundPath = "/api/chat"
	}
	handle := h.store.Start(RequestStart{
		ID:        fmt.Sprintf("playground-%d", startedAt.UnixNano()),
		Model:     input.Model,
		Path:      playgroundPath,
		Consumer:  "local playground",
		StartedAt: startedAt,
	})
	finished := false
	finish := func(usage PlaygroundUsage, problem string) {
		if finished {
			return
		}
		finished = true
		h.store.Finish(handle, RequestFinish{
			CompletedAt:      time.Now().UTC(),
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			Duration:         time.Since(startedAt),
			Error:            problem,
		})
	}
	defer func() { finish(PlaygroundUsage{}, "local playground request did not complete") }()
	if containsImages {
		capabilities, err := h.localModelCapabilities(r.Context(), input.Model)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		if !supportsCapability(capabilities, "vision") {
			writeError(w, http.StatusUnprocessableEntity, errors.New("the selected model does not support images"))
			return
		}
		h.playgroundImageChat(w, r, input, finish)
		return
	}

	var streamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	}
	if input.Stream {
		streamOptions = &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true}
	}
	payload, err := json.Marshal(struct {
		Model         string              `json:"model"`
		Messages      []PlaygroundMessage `json:"messages"`
		Temperature   *float64            `json:"temperature,omitempty"`
		Stream        bool                `json:"stream"`
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options,omitempty"`
	}{Model: input.Model, Messages: input.Messages, Temperature: input.Temperature, Stream: input.Stream, StreamOptions: streamOptions})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	response, err := h.longTextClient().Do(
		ctx,
		http.MethodPost,
		"/v1/chat/completions",
		http.Header{"Content-Type": []string{"application/json"}},
		bytes.NewReader(payload),
	)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("local runtime returned %s", response.Status))
		return
	}
	if input.Stream {
		usage, err := h.streamPlaygroundResponse(w, response.Body, input.Model)
		if err != nil {
			finish(PlaygroundUsage{}, err.Error())
			return
		}
		finish(usage, "")
		return
	}
	var output struct {
		Model   string `json:"model"`
		Choices []struct {
			Message PlaygroundMessage `json:"message"`
		} `json:"choices"`
		Usage PlaygroundUsage `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&output); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("decode local runtime chat response: %w", err))
		return
	}
	if len(output.Choices) == 0 || output.Choices[0].Message.Content == "" {
		writeError(w, http.StatusBadGateway, errors.New("local runtime returned an empty chat response"))
		return
	}
	if output.Model == "" {
		output.Model = input.Model
	}
	finish(output.Usage, "")
	writeJSON(w, http.StatusOK, PlaygroundResponse{Model: output.Model, Content: output.Choices[0].Message.Content, Usage: output.Usage})
}

// canStartPlaygroundModel prevents local chat from bypassing the same memory
// budget shown in the model library. A loaded model is already accounted for;
// a cold model is conservatively estimated from its installed artifact size.
func (h *handler) canStartPlaygroundModel(ctx context.Context, model string) (bool, error) {
	if h.cfg.VRAMTotalGB <= 0 {
		return true, nil
	}
	loaded, err := h.loadedRuntimeModels(ctx)
	if err != nil {
		return false, err
	}
	var loadedBytes int64
	for _, resident := range loaded {
		if resident.Name == model {
			return true, nil
		}
		loadedBytes += resident.SizeVRAM
	}
	installed, err := h.localModels(ctx)
	if err != nil {
		return false, err
	}
	for _, candidate := range installed {
		if candidate.Name != model {
			continue
		}
		available := availableMemoryBytes(h.cfg.VRAMTotalGB, loadedBytes, memoryReserveBytes(h.cfg.VRAMTotalGB))
		return candidate.Size <= available, nil
	}
	// Let the runtime report an unknown model in its own normal response.
	return true, nil
}

func (h *handler) playgroundImageChat(w http.ResponseWriter, r *http.Request, input playgroundInput, finish func(PlaygroundUsage, string)) {
	options := struct {
		Temperature *float64 `json:"temperature,omitempty"`
	}{Temperature: input.Temperature}
	payload, err := json.Marshal(struct {
		Model    string              `json:"model"`
		Messages []PlaygroundMessage `json:"messages"`
		Stream   bool                `json:"stream"`
		Options  any                 `json:"options,omitempty"`
	}{Model: input.Model, Messages: input.Messages, Stream: input.Stream, Options: options})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	response, err := h.longTextClient().Do(
		ctx,
		http.MethodPost,
		"/api/chat",
		http.Header{"Content-Type": []string{"application/json"}},
		bytes.NewReader(payload),
	)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("local runtime returned %s", response.Status))
		return
	}
	if input.Stream {
		usage, err := h.streamImagePlaygroundResponse(w, response.Body, input.Model)
		if err != nil {
			finish(PlaygroundUsage{}, err.Error())
			return
		}
		finish(usage, "")
		return
	}
	var output struct {
		Model           string            `json:"model"`
		Message         PlaygroundMessage `json:"message"`
		PromptEvalCount int               `json:"prompt_eval_count"`
		EvalCount       int               `json:"eval_count"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&output); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("decode local image chat response: %w", err))
		return
	}
	if output.Message.Content == "" {
		writeError(w, http.StatusBadGateway, errors.New("local runtime returned an empty chat response"))
		return
	}
	if output.Model == "" {
		output.Model = input.Model
	}
	usage := PlaygroundUsage{PromptTokens: output.PromptEvalCount, CompletionTokens: output.EvalCount, TotalTokens: output.PromptEvalCount + output.EvalCount}
	finish(usage, "")
	writeJSON(w, http.StatusOK, PlaygroundResponse{Model: output.Model, Content: output.Message.Content, Usage: usage})
}

func (h *handler) streamImagePlaygroundResponse(w http.ResponseWriter, body io.Reader, fallbackModel string) (PlaygroundUsage, error) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	model, usage := fallbackModel, PlaygroundUsage{}
	scanner := bufio.NewScanner(io.LimitReader(body, 16<<20))
	scanner.Buffer(make([]byte, 32<<10), 1<<20)
	for scanner.Scan() {
		var chunk struct {
			Model           string            `json:"model"`
			Message         PlaygroundMessage `json:"message"`
			Done            bool              `json:"done"`
			PromptEvalCount int               `json:"prompt_eval_count"`
			EvalCount       int               `json:"eval_count"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &chunk); err != nil {
			problem := "decode local image chat stream: " + err.Error()
			writePlaygroundSSE(w, playgroundStreamEvent{Type: "error", Error: problem})
			return PlaygroundUsage{}, errors.New(problem)
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Message.Content != "" {
			writePlaygroundSSE(w, playgroundStreamEvent{Type: "delta", Content: chunk.Message.Content})
		}
		if chunk.Done {
			usage = PlaygroundUsage{PromptTokens: chunk.PromptEvalCount, CompletionTokens: chunk.EvalCount, TotalTokens: chunk.PromptEvalCount + chunk.EvalCount}
		}
	}
	if err := scanner.Err(); err != nil {
		problem := "read local image chat stream: " + err.Error()
		writePlaygroundSSE(w, playgroundStreamEvent{Type: "error", Error: problem})
		return PlaygroundUsage{}, errors.New(problem)
	}
	writePlaygroundSSE(w, playgroundStreamEvent{Type: "done", Model: model, Usage: usage})
	return usage, nil
}

func (h *handler) streamPlaygroundResponse(w http.ResponseWriter, body io.Reader, fallbackModel string) (PlaygroundUsage, error) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	model, usage := fallbackModel, PlaygroundUsage{}
	scanner := bufio.NewScanner(io.LimitReader(body, 16<<20))
	scanner.Buffer(make([]byte, 32<<10), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta PlaygroundMessage `json:"delta"`
			} `json:"choices"`
			Usage PlaygroundUsage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			problem := "decode local runtime chat stream: " + err.Error()
			writePlaygroundSSE(w, playgroundStreamEvent{Type: "error", Error: problem})
			return PlaygroundUsage{}, errors.New(problem)
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			usage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				writePlaygroundSSE(w, playgroundStreamEvent{Type: "delta", Content: choice.Delta.Content})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		problem := "read local runtime chat stream: " + err.Error()
		writePlaygroundSSE(w, playgroundStreamEvent{Type: "error", Error: problem})
		return PlaygroundUsage{}, errors.New(problem)
	}
	writePlaygroundSSE(w, playgroundStreamEvent{Type: "done", Model: model, Usage: usage})
	return usage, nil
}

func writePlaygroundSSE(w http.ResponseWriter, event playgroundStreamEvent) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
