package bridge

type openAIEmbeddingRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"`
	User  string `json:"user,omitempty"`
}

type openAIEmbeddingResponse struct {
	Object string            `json:"object"`
	Model  string            `json:"model"`
	Data   []openAIEmbedding `json:"data"`
	Usage  openAIUsage       `json:"usage"`
}

type openAIEmbedding struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type openAIUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type minimaxEmbeddingRequest struct {
	Model string   `json:"model"`
	Type  string   `json:"type"`
	Texts []string `json:"texts"`
}

type minimaxEmbeddingResponse struct {
	Vectors  [][]float64     `json:"vectors"`
	BaseResp minimaxBaseResp `json:"base_resp"`
}

type minimaxBaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}
