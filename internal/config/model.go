package config

// 对话模型配置
type ModelConfig struct {
	ModelName string
	Provider  string
	TopK      float64
	TopP      float64
	Temp      float64
	Tinking   bool
}
