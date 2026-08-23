package runtime

import "context"

type RerankHealth = RuntimeHealth

type RerankClient struct {
	target *Target
}

func NewRerankClient(baseURL string, client HTTPDoer) *RerankClient {
	return &RerankClient{target: newTarget(KindRerank, baseURL, client)}
}

func (c *RerankClient) Health(ctx context.Context) (RerankHealth, error) {
	return fetchHealth(ctx, c.target)
}
