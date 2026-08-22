// Mirror of backend/pkg/edge/types.go — see protocol.go for the canonical-source policy.
package protocol

type RuntimeKind string

const (
	RuntimeText   RuntimeKind = "text"
	RuntimeImage  RuntimeKind = "image"
	RuntimeSpeech RuntimeKind = "speech"
)

type CapabilityID string

const (
	CapabilityTextChat       CapabilityID = "text.chat"
	CapabilityTextCompletion CapabilityID = "text.completion"
	CapabilityTextResponses  CapabilityID = "text.responses"
	CapabilityTextEmbedding  CapabilityID = "text.embedding"
	CapabilityTextVision     CapabilityID = "text.vision"
	CapabilityImageGenerate  CapabilityID = "image.generate"
	CapabilityImageEdit      CapabilityID = "image.edit"
	CapabilityAudioTTS       CapabilityID = "audio.tts"
)

type CapabilityStatus string

const (
	CapabilityReady       CapabilityStatus = "ready"
	CapabilityWarming     CapabilityStatus = "warming"
	CapabilityDegraded    CapabilityStatus = "degraded"
	CapabilityUnavailable CapabilityStatus = "unavailable"
	CapabilityUnsupported CapabilityStatus = "unsupported"
)

type CapabilityLimits struct {
	MaxInputBytes      int64    `json:"max_input_bytes,omitempty"`
	MaxInputCharacters int      `json:"max_input_characters,omitempty"`
	Formats            []string `json:"formats,omitempty"`
}

type Capability struct {
	ID      CapabilityID     `json:"id"`
	Runtime RuntimeKind      `json:"runtime"`
	Status  CapabilityStatus `json:"status"`
	Models  []string         `json:"models,omitempty"`
	Paths   []string         `json:"paths,omitempty"`
	Version string           `json:"version,omitempty"`
	Reason  string           `json:"reason,omitempty"`
	Limits  CapabilityLimits `json:"limits,omitempty"`
}

type Hardware struct {
	GPUModel    string `json:"gpu_model,omitempty"`
	GPUCount    int    `json:"gpu_count,omitempty"`
	VRAMTotalGB int    `json:"vram_total_gb,omitempty"`
	CUDAVersion string `json:"cuda_version,omitempty"`
	Driver      string `json:"driver,omitempty"`
	CPUModel    string `json:"cpu_model,omitempty"`
	RAMTotalGB  int    `json:"ram_total_gb,omitempty"`
	Platform    string `json:"platform,omitempty"`
}

type Location struct {
	CountryISO2 string  `json:"country_iso2,omitempty"`
	Region      string  `json:"region,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
}

type NodeMeta struct {
	Name         string       `json:"name"`
	Hardware     Hardware     `json:"hardware"`
	Location     Location     `json:"location"`
	Models       []string     `json:"models"`
	Capabilities []Capability `json:"capabilities,omitempty"`
	Workloads    []string     `json:"workloads,omitempty"`
	AgentVer     string       `json:"agent_version"`
	UpdatedAt    int64        `json:"updated_at,omitempty"`
}

// KnownWorkloads mirrors backend/pkg/edge.AllWorkloads. The agent validates EVERYAPI_WORKLOADS against this list at startup so a typo fails fast on the supplier's machine instead of being silently dropped by the gateway.
var KnownWorkloads = []string{
	"chat",
	"coding",
	"image",
	"video",
	"audio",
	"render",
	"embedding",
}
