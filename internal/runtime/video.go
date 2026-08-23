package runtime

import "context"

type VideoHealth = RuntimeHealth

type VideoClient struct {
	target *Target
}

func NewVideoClient(baseURL string, client HTTPDoer) *VideoClient {
	return &VideoClient{target: newTarget(KindVideo, baseURL, client)}
}

func (c *VideoClient) Health(ctx context.Context) (VideoHealth, error) {
	return fetchHealth(ctx, c.target)
}
