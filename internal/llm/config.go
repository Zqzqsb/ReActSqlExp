package llm

import (
	"encoding/json"
	"os"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

// ModelConfig LLM model config
type ModelConfig struct {
	ModelName       string `json:"model_name"`
	Token           string `json:"token"`
	BaseURL         string `json:"base_url"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// ConfigFile config file structure
type ConfigFile struct {
	DeepSeekV3     ModelConfig `json:"deepseek_v3"`
	DeepSeekV32    ModelConfig `json:"deepseek_v3_2"`
	QwenMax        ModelConfig `json:"qwen_max"`
	Qwen3Max       ModelConfig `json:"qwen3_max"`
	Qwen35         ModelConfig `json:"qwen3.5"`
	AliDeepSeek    ModelConfig `json:"ali_deepseek_v3_2"`
	DoubaoSeed2Pro  ModelConfig `json:"doubao_seed2_pro"`
	Qwen3CoderPlus ModelConfig `json:"qwen3_coder_plus"`
}

var (
	// Global config (loaded from file)
	config *ConfigFile
)

func init() {
	// Try loading config file
	var err error
	config, err = loadConfig()
	if err != nil {
		panic("Failed to load llm_config.json: " + err.Error() + ". Please create llm_config.json in the project root.")
	}
}

// loadConfig loads config file
func loadConfig() (*ConfigFile, error) {
	// Try multiple possible config paths
	paths := []string{
		"llm_config.json",
		"../llm_config.json",
		"../../llm_config.json",
	}

	var lastErr error
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			lastErr = err
			continue
		}

		var cfg ConfigFile
		if err := json.Unmarshal(data, &cfg); err != nil {
			lastErr = err
			continue
		}

		return &cfg, nil
	}

	// Return error if config not found
	return nil, lastErr
}

// GetConfig gets current config
func GetConfig() *ConfigFile {
	if config == nil {
		panic("LLM config not initialized. Please ensure llm_config.json exists.")
	}
	return config
}

// GetModel gets model config by flag
func GetModel(useV32 bool) ModelConfig {
	cfg := GetConfig()
	if useV32 {
		return cfg.DeepSeekV32
	}
	return cfg.DeepSeekV3
}

// GetModelName gets model display name
func GetModelName(useV32 bool) string {
	if useV32 {
		return "DeepSeek-V3.2"
	}
	return "DeepSeek-V3"
}

// CreateLLM creates LLM instance
func CreateLLM(config ModelConfig) (llms.Model, error) {
	return openai.New(
		openai.WithModel(config.ModelName),
		openai.WithToken(config.Token),
		openai.WithBaseURL(config.BaseURL),
	)
}

// CreateLLMWithFlag creates LLM by flag
func CreateLLMWithFlag(useV32 bool) (llms.Model, error) {
	modelConfig := GetModel(useV32)
	return CreateLLM(modelConfig)
}

// ModelType model type enum
type ModelType string

const (
	ModelDeepSeekV3     ModelType = "deepseek-v3"
	ModelDeepSeekV32    ModelType = "deepseek-v3.2"
	ModelQwenMax        ModelType = "qwen-max"
	ModelQwen3Max       ModelType = "qwen3-max"
	ModelQwen35         ModelType = "qwen3.5"
	ModelAliDeepSeekV32 ModelType = "ali-deepseek-v3.2"
	ModelDoubaoSeed2Pro ModelType = "doubao-seed2-pro"
	ModelQwen3CoderPlus ModelType = "qwen3-coder-plus"
)

// GetModelByType gets config by model type
func GetModelByType(modelType ModelType) ModelConfig {
	cfg := GetConfig()
	switch modelType {
	case ModelDeepSeekV3:
		return cfg.DeepSeekV3
	case ModelDeepSeekV32:
		return cfg.DeepSeekV32
	case ModelQwenMax:
		return cfg.QwenMax
	case ModelQwen3Max:
		return cfg.Qwen3Max
	case ModelQwen35:
		return cfg.Qwen35
	case ModelAliDeepSeekV32:
		return cfg.AliDeepSeek
	case ModelDoubaoSeed2Pro:
		return cfg.DoubaoSeed2Pro
	case ModelQwen3CoderPlus:
		return cfg.Qwen3CoderPlus
	default:
		return cfg.DeepSeekV3
	}
}

// GetModelDisplayName gets model display name
func GetModelDisplayName(modelType ModelType) string {
	switch modelType {
	case ModelDeepSeekV3:
		return "DeepSeek-V3 (Volcano)"
	case ModelDeepSeekV32:
		return "DeepSeek-V3.2 (Volcano)"
	case ModelQwenMax:
		return "Qwen-Max (Aliyun)"
	case ModelQwen3Max:
		return "Qwen3-Max (Aliyun)"
	case ModelQwen35:
		return "Qwen3.5 (Aliyun)"
	case ModelAliDeepSeekV32:
		return "DeepSeek-V3.2 (Aliyun)"
	case ModelDoubaoSeed2Pro:
		return "Doubao-Seed2-Pro (Volcano)"
	case ModelQwen3CoderPlus:
		return "Qwen3-Coder-Plus (Aliyun)"
	default:
		return "Unknown"
	}
}

// CreateLLMByType creates LLM by model type
func CreateLLMByType(modelType ModelType) (llms.Model, error) {
	modelConfig := GetModelByType(modelType)
	return CreateLLM(modelConfig)
}
