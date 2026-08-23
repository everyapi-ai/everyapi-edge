package runtime

import "context"

type RenderHealth = RuntimeHealth

type RenderClient struct {
	target *Target
}

func NewRenderClient(baseURL string, client HTTPDoer) *RenderClient {
	return &RenderClient{target: newTarget(KindRender, baseURL, client)}
}

func (c *RenderClient) Health(ctx context.Context) (RenderHealth, error) {
	return fetchHealth(ctx, c.target)
}
