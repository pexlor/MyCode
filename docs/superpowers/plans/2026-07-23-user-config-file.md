# 用户级配置文件实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 让 MyCode 从 `~/.mycode/config.yaml` 加载模型、摘要模型和上下文配置，同时允许已有环境变量覆盖文件值。

**架构：** 在 `internal/config` 中建立独立的配置加载边界，依次完成 YAML 解码、默认值、环境变量覆盖、校验和权限警告。REPL 只接收已解析的配置来组装运行时，并用最终配置显示欢迎信息，不再直接读取模型相关环境变量。

**技术栈：** Go 1.24、`gopkg.in/yaml.v3`、标准库 `os`/`path/filepath`、Go table-driven tests。

---

## 文件结构

- 创建 `internal/config/config.go`：定义 YAML 配置结构、默认值和校验。
- 创建 `internal/config/loader.go`：定位用户配置、读取 YAML、覆盖环境变量并输出权限警告。
- 创建 `internal/config/loader_test.go`：覆盖加载、覆盖优先级、错误和权限场景。
- 删除 `internal/config/model.go`：移除未被引用且会与新配置模型重名的旧类型。
- 修改 `internal/repl/ui.go`：消费最终配置，删除散落的环境变量解析，并展示实际模型名。
- 创建 `internal/repl/ui_test.go`：验证运行时参数映射和欢迎页模型展示。
- 修改 `docs/superpowers/specs/2026-07-23-user-config-file-design.md`：修正上下文输出预留对应的历史环境变量名称。

### 任务 1：实现配置结构、YAML 加载和校验

**文件：**
- 创建：`internal/config/config.go`
- 创建：`internal/config/loader.go`
- 创建：`internal/config/loader_test.go`
- 删除：`internal/config/model.go`

- [ ] **步骤 1：编写配置加载失败测试**

创建 `internal/config/loader_test.go`，先覆盖完整 YAML、缺失文件、非法 YAML 和权限警告：

```go
package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFile(t *testing.T) {
	t.Setenv("MYCODE_PROTOCOL", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("MYCODE_BASE_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("MYCODE_API_KEY", "")
	t.Setenv("MYCODE_MODEL", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("model:\n  protocol: anthropic\n  base_url: https://api.example.com\n  api_key: secret\n  name: model-a\n  max_tokens: 4096\ncontext:\n  window: 200000\n  output_reserve: 8192\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(path, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model.Name != "model-a" || got.Model.MaxTokens != 4096 || got.Context.Window != 200000 {
		t.Fatalf("config = %#v", got)
	}
}

func TestLoadFileErrorsIncludePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	if _, err := LoadFile(path, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v", err)
	}
	if err := os.WriteFile(path, []byte("model: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadFileWarnsForBroadPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("model:\n  protocol: anthropic\n  base_url: https://api.example.com\n  api_key: secret\n  name: model-a\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	var warnings bytes.Buffer
	if _, err := LoadFile(path, &warnings); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warnings.String(), "chmod 600") {
		t.Fatalf("warning = %q", warnings.String())
	}
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/config -run 'TestLoadFile' -v`

预期：FAIL，编译错误包含 `undefined: LoadFile`。

- [ ] **步骤 3：实现配置类型、默认值和文件加载**

创建 `internal/config/config.go`：

```go
package config

import (
	"fmt"
	"strings"
)

const (
	DefaultContextWindow = 128000
	DefaultOutputReserve = 8192
)

type Config struct {
	Model   ModelConfig   `yaml:"model"`
	Summary SummaryConfig `yaml:"summary"`
	Context ContextConfig `yaml:"context"`
}

type ModelConfig struct {
	Protocol  string `yaml:"protocol"`
	BaseURL   string `yaml:"base_url"`
	APIKey    string `yaml:"api_key"`
	Name      string `yaml:"name"`
	MaxTokens int    `yaml:"max_tokens"`
}

type SummaryConfig struct {
	Model   string `yaml:"model"`
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
}

type ContextConfig struct {
	Window        int `yaml:"window"`
	OutputReserve int `yaml:"output_reserve"`
}

func (c *Config) applyDefaults() {
	if c.Context.Window == 0 {
		c.Context.Window = DefaultContextWindow
	}
	if c.Context.OutputReserve == 0 {
		c.Context.OutputReserve = DefaultOutputReserve
	}
	if c.Summary.Model != "" && strings.TrimSpace(c.Summary.BaseURL) == "" {
		c.Summary.BaseURL = c.Model.BaseURL
	}
}

func (c Config) validate() error {
	required := []struct{ name, value string }{
		{"model.protocol", c.Model.Protocol}, {"model.base_url", c.Model.BaseURL},
		{"model.api_key", c.Model.APIKey}, {"model.name", c.Model.Name},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	if c.Model.MaxTokens < 0 || c.Context.Window <= 0 || c.Context.OutputReserve <= 0 {
		return fmt.Errorf("model.max_tokens must be non-negative and context values must be positive")
	}
	if c.Summary.Model != "" && strings.TrimSpace(c.Summary.APIKey) == "" {
		return fmt.Errorf("summary.api_key is required when summary.model is set")
	}
	return nil
}
```

用现有 `internal/config/model.go` 中未被引用的旧 `ModelConfig` 定义替换为上述单一配置模型，避免同包重名。创建 `internal/config/loader.go`：

```go
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".mycode", "config.yaml"), nil
}

func Load(warnings io.Writer) (Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return Config{}, err
	}
	return LoadFile(path, warnings)
}

func LoadFile(path string, warnings io.Writer) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var result Config
	if err := yaml.Unmarshal(data, &result); err != nil {
		return Config{}, fmt.Errorf("decode config %s: %w", path, err)
	}
	result.applyDefaults()
	if err := applyEnvironment(&result); err != nil {
		return Config{}, err
	}
	result.applyDefaults()
	if err := result.validate(); err != nil {
		return Config{}, fmt.Errorf("validate config %s: %w", path, err)
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 && warnings != nil {
		fmt.Fprintf(warnings, "warning: config %s is readable by other users; run chmod 600 %s\n", path, path)
	}
	return result, nil
}
```

- [ ] **步骤 4：运行加载测试验证通过**

运行：`go test ./internal/config -run 'TestLoadFile' -v`

预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/config/config.go internal/config/loader.go internal/config/loader_test.go internal/config/model.go
git commit -m "feat: load user configuration from yaml"
```

### 任务 2：实现环境变量覆盖和严格整数解析

**文件：**
- 修改：`internal/config/loader.go`
- 修改：`internal/config/loader_test.go`

- [ ] **步骤 1：编写覆盖优先级和非法整数测试**

在 `internal/config/loader_test.go` 增加：

```go
func TestEnvironmentOverridesFile(t *testing.T) {
	t.Setenv("MYCODE_PROTOCOL", "openai-compat")
	t.Setenv("MYCODE_BASE_URL", "https://mycode.example.com")
	t.Setenv("ANTHROPIC_BASE_URL", "https://protocol.example.com")
	t.Setenv("MYCODE_API_KEY", "mycode-key")
	t.Setenv("ANTHROPIC_API_KEY", "protocol-key")
	t.Setenv("MYCODE_MODEL", "model-b")
	t.Setenv("MYCODE_MAX_TOKENS", "2048")
	t.Setenv("MYCODE_CONTEXT_WINDOW", "64000")
	t.Setenv("MYCODE_MAX_OUTPUT_TOKENS", "4096")
	path := writeValidConfig(t)
	got, err := LoadFile(path, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model.BaseURL != "https://protocol.example.com" || got.Model.APIKey != "protocol-key" {
		t.Fatalf("protocol-specific precedence failed: %#v", got.Model)
	}
	if got.Model.Protocol != "openai-compat" || got.Model.Name != "model-b" || got.Model.MaxTokens != 2048 {
		t.Fatalf("model overrides = %#v", got.Model)
	}
	if got.Context.Window != 64000 || got.Context.OutputReserve != 4096 {
		t.Fatalf("context overrides = %#v", got.Context)
	}
}

func TestInvalidEnvironmentIntegerIsAnError(t *testing.T) {
	t.Setenv("MYCODE_CONTEXT_WINDOW", "large")
	_, err := LoadFile(writeValidConfig(t), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "MYCODE_CONTEXT_WINDOW") || !strings.Contains(err.Error(), "large") {
		t.Fatalf("error = %v", err)
	}
}

func writeValidConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("model:\n  protocol: anthropic\n  base_url: https://api.example.com\n  api_key: secret\n  name: model-a\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/config -run 'TestEnvironment|TestInvalidEnvironment' -v`

预期：FAIL，编译错误包含 `undefined: applyEnvironment`，或断言显示文件值未被覆盖。

- [ ] **步骤 3：实现环境变量映射**

在 `internal/config/loader.go` 增加 `strconv` 和 `strings` import，并实现：

```go
func applyEnvironment(c *Config) error {
	c.Model.Protocol = envString(c.Model.Protocol, "MYCODE_PROTOCOL")
	c.Model.BaseURL = envString(c.Model.BaseURL, "MYCODE_BASE_URL", "ANTHROPIC_BASE_URL")
	c.Model.APIKey = envString(c.Model.APIKey, "MYCODE_API_KEY", "ANTHROPIC_API_KEY")
	c.Model.Name = envString(c.Model.Name, "MYCODE_MODEL")
	c.Summary.Model = envString(c.Summary.Model, "MYCODE_SUMMARY_MODEL")
	c.Summary.BaseURL = envString(c.Summary.BaseURL, "MYCODE_SUMMARY_BASE_URL")
	c.Summary.APIKey = envString(c.Summary.APIKey, "MYCODE_SUMMARY_API_KEY")
	for _, item := range []struct {
		name string
		dst  *int
	}{
		{"MYCODE_MAX_TOKENS", &c.Model.MaxTokens},
		{"MYCODE_CONTEXT_WINDOW", &c.Context.Window},
		{"MYCODE_MAX_OUTPUT_TOKENS", &c.Context.OutputReserve},
	} {
		value := strings.TrimSpace(os.Getenv(item.name))
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return fmt.Errorf("environment variable %s has invalid positive integer %q", item.name, value)
		}
		*item.dst = parsed
	}
	return nil
}

func envString(fallback string, names ...string) string {
	result := fallback
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			result = value
		}
	}
	return result
}
```

这里按参数从低到高排列优先级，因此 `ANTHROPIC_*` 覆盖对应的 `MYCODE_*` 历史变量。

- [ ] **步骤 4：运行配置包全部测试**

运行：`go test ./internal/config -v`

预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/config/loader.go internal/config/loader_test.go
git commit -m "feat: support environment config overrides"
```

### 任务 3：将最终配置接入 REPL 运行时

**文件：**
- 修改：`internal/repl/ui.go`
- 创建：`internal/repl/ui_test.go`

- [ ] **步骤 1：编写运行时映射和欢迎页测试**

创建 `internal/repl/ui_test.go`：

```go
package repl

import (
	"MyCode/internal/config"
	"bytes"
	"strings"
	"testing"
)

func TestModelParametersFromConfig(t *testing.T) {
	cfg := config.Config{Model: config.ModelConfig{
		Protocol: "anthropic", BaseURL: "https://api.example.com", APIKey: "secret",
		Name: "model-a", MaxTokens: 4096,
	}}
	got := modelParameters(cfg.Model)
	if got.Protocol != "anthropic" || got.ModelName != "model-a" || got.MaxToken != 4096 {
		t.Fatalf("parameters = %#v", got)
	}
}

func TestPrintWelcomeUsesConfiguredModel(t *testing.T) {
	var output bytes.Buffer
	printWelcomeTo(&output, "model-a")
	if !strings.Contains(output.String(), "model: model-a") {
		t.Fatalf("output = %q", output.String())
	}
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/repl -run 'TestModelParameters|TestPrintWelcome' -v`

预期：FAIL，编译错误包含 `undefined: modelParameters`，且 `printWelcomeTo` 参数数量不匹配。

- [ ] **步骤 3：重构启动流程消费配置对象**

修改 `internal/repl/ui.go`：

```go
import appconfig "MyCode/internal/config"

func runInteractive() {
	cfg, err := appconfig.Load(os.Stderr)
	if err != nil {
		printError("配置加载失败", err)
		return
	}
	reader := bufio.NewReader(os.Stdin)
	printWelcomeTo(os.Stdout, cfg.Model.Name)

	systemPrompt, err := prompt.BuildSystemPrompt()
	if err != nil {
		printError("消息初始化失败", err)
		return
	}
	runtime, err := initAgent(cfg)
	if err != nil {
		printError("agent 初始化失败", err)
		return
	}
	defer runtime.cleanup()

	// 此处之后接回当前函数从 session.NewService 开始的命令循环。
}

func modelParameters(model appconfig.ModelConfig) *llm.ModelParm {
	return &llm.ModelParm{
		Protocol: model.Protocol, BaseURL: model.BaseURL, APIKey: model.APIKey,
		ModelName: model.Name, MaxToken: int64(model.MaxTokens),
	}
}

func initAgent(cfg appconfig.Config) (*agentRuntime, error) {
	client, err := llm.NewClient(modelParameters(cfg.Model))
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	tools, cleanup, err := tool.CreateDefaultToolsWithMCP(ctx)
	if err != nil {
		return nil, err
	}
	runner, err := agent.NewAgent(ctx, client, tools)
	if err != nil {
		cleanup()
		return nil, err
	}
	workspace, err := os.Getwd()
	if err != nil {
		cleanup()
		return nil, err
	}
	store, err := contextmanager.NewFileConversationStore(filepath.Join(workspace, ".context", "sessions"))
	if err != nil {
		cleanup()
		return nil, err
	}
	var primary contextmanager.Summarizer
	if cfg.Summary.Model != "" {
		summaryClient, summaryErr := llm.NewClient(&llm.ModelParm{
			Protocol: cfg.Model.Protocol, BaseURL: cfg.Summary.BaseURL,
			APIKey: cfg.Summary.APIKey, ModelName: cfg.Summary.Model,
			MaxToken: int64(cfg.Model.MaxTokens),
		})
		if summaryErr != nil {
			cleanup()
			return nil, summaryErr
		}
		primary = contextmanager.LLMSummarizer{Client: summaryClient}
	}
	contextManager, err := contextmanager.NewContextManager(contextmanager.ContextManagerConfig{
		Store: store, Estimator: contextmanager.ConservativeEstimator{}, Policy: contextmanager.DefaultPolicy(),
		Model: contextmanager.ModelContextSpec{
			ModelName: cfg.Model.Name, ContextWindow: cfg.Context.Window,
			MaxOutputTokens: cfg.Context.OutputReserve,
		},
		Workspace: workspace, Primary: primary,
		Fallback: contextmanager.LLMSummarizer{Client: client},
	})
	if err != nil {
		cleanup()
		return nil, err
	}
	return &agentRuntime{runner: runner, contextManager: contextManager, store: store, workspace: workspace, cleanup: cleanup}, nil
}

func printWelcomeTo(out io.Writer, modelName string) {
	fmt.Fprintln(out, colorCyan+"MyCode CLI"+colorReset)
	fmt.Fprintf(out, "%smodel: %s | /help for commands | /exit to quit%s\n\n", colorDim, modelName, colorReset)
}
```

在现有命令循环中将清屏回调里的 `printWelcomeTo(out)` 精确替换为 `printWelcomeTo(out, cfg.Model.Name)`。删除无参数的 `printWelcome`、`envOrDefault`、`requiredEnv`、`envInt` 和不再使用的 `strconv` import。主模型地址、凭据和模型名全部来自 `cfg`。

- [ ] **步骤 4：运行 REPL 测试验证通过**

运行：`go test ./internal/repl -run 'TestModelParameters|TestPrintWelcome' -v`

预期：PASS。

- [ ] **步骤 5：运行 REPL 包全部测试**

运行：`go test ./internal/repl -v`

预期：PASS，现有 session 命令测试无回归。

- [ ] **步骤 6：Commit**

```bash
git add internal/repl/ui.go internal/repl/ui_test.go
git commit -m "feat: initialize repl from user config"
```

### 任务 4：补齐验证并同步文档

**文件：**
- 修改：`docs/superpowers/specs/2026-07-23-user-config-file-design.md`

- [ ] **步骤 1：确认规格中的环境变量映射与实现一致**

运行：

```bash
rg -n 'MYCODE_(MAX_TOKENS|CONTEXT_WINDOW|MAX_OUTPUT_TOKENS)' \
  internal/config docs/superpowers/specs/2026-07-23-user-config-file-design.md
```

预期：三个变量均同时出现在加载器、测试和规格中；上下文输出预留使用 `MYCODE_MAX_OUTPUT_TOKENS`。

- [ ] **步骤 2：格式化代码并检查差异**

运行：`gofmt -w internal/config/config.go internal/config/loader.go internal/config/loader_test.go internal/repl/ui.go internal/repl/ui_test.go`

运行：`git diff --check`

预期：`git diff --check` 无输出并以 0 退出。

- [ ] **步骤 3：运行完整测试和静态检查**

运行：`go test ./...`

预期：PASS，所有包返回 `ok` 或 `[no test files]`。

运行：`go vet ./...`

预期：无输出并以 0 退出。

- [ ] **步骤 4：提交规格修正和格式化产生的必要变更**

```bash
git add docs/superpowers/specs/2026-07-23-user-config-file-design.md
git commit -m "docs: correct config environment mapping"
```

如果 `gofmt` 仅格式化了前三个任务已经提交的 Go 文件，则将这些纯格式化变更与本步骤一起提交并使用提交信息 `chore: format user config implementation`；若没有产生差异则只提交规格修正。
