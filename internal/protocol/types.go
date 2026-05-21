// Mirror of backend/pkg/edge/types.go — see protocol.go for the
// canonical-source policy.
package protocol

type Hardware struct {
	GPUModel     string `json:"gpu_model,omitempty"`
	GPUCount     int    `json:"gpu_count,omitempty"`
	VRAMTotalGB  int    `json:"vram_total_gb,omitempty"`
	CUDAVersion  string `json:"cuda_version,omitempty"`
	Driver       string `json:"driver,omitempty"`
	CPUModel     string `json:"cpu_model,omitempty"`
	RAMTotalGB   int    `json:"ram_total_gb,omitempty"`
	Platform     string `json:"platform,omitempty"`
}

type Location struct {
	CountryISO2 string  `json:"country_iso2,omitempty"`
	Region      string  `json:"region,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
}

type NodeMeta struct {
	Name      string   `json:"name"`
	Hardware  Hardware `json:"hardware"`
	Location  Location `json:"location"`
	Models    []string `json:"models"`
	AgentVer  string   `json:"agent_version"`
	UpdatedAt int64    `json:"updated_at,omitempty"`
}
