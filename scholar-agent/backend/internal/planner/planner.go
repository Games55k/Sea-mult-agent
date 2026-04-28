package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"scholar-agent-backend/internal/appconfig"
	"scholar-agent-backend/internal/models"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// Planner generates DAGs based on user intent using LLM
type Planner struct {
	llmBaseURL string
	llmAPIKey  string
	llmModel   string
}

func NewPlanner() *Planner {
	llmCfg, err := appconfig.LoadLLMConfig()
	if err != nil {
		log.Printf("[Planner] 加载 LLM 配置失败，将禁用描述增强: %v", err)
		return &Planner{}
	}
	return &Planner{
		llmBaseURL: llmCfg.BaseURL,
		llmAPIKey:  llmCfg.APIKey,
		llmModel:   llmCfg.Model,
	}
}

// frameworkInfo 保存从用户意图中提取到的框架信息
type frameworkInfo struct {
	FrameworkA string `json:"framework_a"`
	FrameworkB string `json:"framework_b"`
	UseCase    string `json:"use_case"`
}

var frameworkPackageMap = map[string][]string{
	"langchain":       {"langchain", "langchain-community", "langchain-core"},
	"llamaindex":      {"llama-index"},
	"llama_index":     {"llama-index"},
	"llama-index":     {"llama-index"},
	"haystack":        {"haystack-ai"},
	"dspy":            {"dspy-ai"},
	"autogen":         {"pyautogen"},
	"crewai":          {"crewai"},
	"langgraph":       {"langgraph"},
	"semantic kernel": {"semantic-kernel"},
	"spring ai":       {"spring-ai"},
	"openai":          {"openai"},
	"cohere":          {"cohere"},
}

// extractFrameworks 使用 LLM 或规则从用户意图中提取框架名称
func (p *Planner) extractFrameworks(intent string) frameworkInfo {
	// 先用规则快速提取常见框架
	known := []string{"langchain", "llamaindex", "llama_index", "llama-index",
		"haystack", "dspy", "autogen", "crewai", "langgraph",
		"semantic kernel", "spring ai", "openai", "cohere"}
	found := []string{}
	lower := strings.ToLower(intent)
	for _, fw := range known {
		if strings.Contains(lower, fw) {
			// 标准化名称
			switch fw {
			case "llama_index", "llama-index":
				found = append(found, "LlamaIndex")
			default:
				found = append(found, strings.Title(fw))
			}
		}
	}

	// 推断 use case
	useCase := "RAG 问答"
	if strings.Contains(lower, "agent") || strings.Contains(lower, "智能体") {
		useCase = "Agent 构建"
	} else if strings.Contains(lower, "rag") || strings.Contains(lower, "检索") {
		useCase = "RAG 问答"
	} else if strings.Contains(lower, "tool") || strings.Contains(lower, "工具") {
		useCase = "Tool Use"
	}

	info := frameworkInfo{UseCase: useCase}
	if len(found) >= 2 {
		info.FrameworkA = found[0]
		info.FrameworkB = found[1]
	} else if len(found) == 1 {
		info.FrameworkA = found[0]
		info.FrameworkB = "LlamaIndex"
		if strings.EqualFold(found[0], "LlamaIndex") {
			info.FrameworkB = "LangChain"
		}
	} else {
		// 默认：对比 LangChain 和 LlamaIndex
		info.FrameworkA = "LangChain"
		info.FrameworkB = "LlamaIndex"
	}
	return info
}

func normalizeFrameworkName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func frameworkPackages(name string) []string {
	key := normalizeFrameworkName(name)
	if packages, ok := frameworkPackageMap[key]; ok {
		return packages
	}
	return []string{key}
}

func frameworkTaskDescription(name, useCase, intent string) string {
	packages := frameworkPackages(name)
	packageList := strings.Join(packages, " ")
	primaryPackage := packages[0]

	return fmt.Sprintf(`请编写一个完整的 Python 脚本，在 Docker 沙箱（python:3.9-bullseye）中演示使用该目标框架实现 "%s" 功能。

目标框架：%s
建议安装包：%s

关键要求：
1. 必须在脚本开头使用 subprocess 安装所有依赖（至少安装目标框架的必要包，例如 %s）
2. 使用随机生成的 Dummy 数据或本地构造样例，不依赖外部数据集、私有密钥或远程 API
3. 运行结束后打印关键指标（如耗时、输出摘要、是否成功）
4. 将结果以 JSON 格式打印到最后一行，格式：{"framework": "%s", "latency_ms": 数字, "output_preview": "字符串"}
5. 脚本逻辑必须自洽，可解释，不能为通过测试而硬编码结果

原始需求：%s`, useCase, name, packageList, primaryPackage, name, intent)
}

// llmExtractPaperName 从意图中提取论文名称
func (p *Planner) llmExtractPaperName(intent string) string {
	// 尝试匹配引号内的论文名
	re := regexp.MustCompile(`["'《](.+?)["'》]`)
	if m := re.FindStringSubmatch(intent); len(m) > 1 {
		return m[1]
	}
	// 匹配"复现xxx论文"中的xxx
	re2 := regexp.MustCompile(`复现\s*(.{5,50}?)(?:论文|的|$)`)
	if m := re2.FindStringSubmatch(intent); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return intent
}

// GeneratePlan creates a DAG for a given intent, using LLM to tailor task descriptions
func (p *Planner) GeneratePlan(intent string, intentType string) (*models.Plan, error) {
	planID := uuid.New().String()

	plan := &models.Plan{
		ID:         planID,
		UserIntent: intent,
		Status:     models.StatusPending,
		Tasks:      make(map[string]*models.Task),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	switch intentType {
	case "Framework_Evaluation":
		fw := p.extractFrameworks(intent)
		p.buildFrameworkEvalPlan(plan, intent, fw)

	case "Paper_Reproduction":
		paperName := p.llmExtractPaperName(intent)
		p.buildPaperReproductionPlan(plan, intent, paperName)

	case "Code_Execution":
		p.buildCodeExecutionPlan(plan, intent)

	default:
		t1 := createTask(
			"处理用户请求",
			"librarian_agent",
			nil,
			fmt.Sprintf("请回答以下问题或处理请求：\n%s", intent),
		)
		plan.Tasks[t1.ID] = t1
	}

	// 如果 API Key 存在，尝试用 LLM 优化任务描述
	if p.llmAPIKey != "" && intentType != "Code_Execution" {
		p.enrichTaskDescriptions(plan, intent)
	}

	return plan, nil
}

// buildFrameworkEvalPlan 构建框架评测 DAG
func (p *Planner) buildFrameworkEvalPlan(plan *models.Plan, intent string, fw frameworkInfo) {
	t1 := createTask(
		fmt.Sprintf("检索 %s 与 %s 的文档与最佳实践", fw.FrameworkA, fw.FrameworkB),
		"librarian_agent",
		nil,
		fmt.Sprintf(`请对以下两个框架进行深度调研，围绕"%s"场景，提供：
1. 两个框架的核心架构、适用场景、优劣势对比
2. 各框架实现 %s 的典型代码模式（关键 API）
3. 安装命令与常见依赖版本

框架 A: %s
框架 B: %s
用户原始需求: %s`, fw.UseCase, fw.UseCase, fw.FrameworkA, fw.FrameworkB, intent),
	)

	t2 := createTask(
		fmt.Sprintf("在沙箱中安装并运行 %s 示例代码", fw.FrameworkA),
		"coder_agent",
		[]string{t1.ID},
		frameworkTaskDescription(fw.FrameworkA, fw.UseCase, intent),
	)

	t3 := createTask(
		fmt.Sprintf("在沙箱中安装并运行 %s 示例代码", fw.FrameworkB),
		"coder_agent",
		[]string{t1.ID},
		frameworkTaskDescription(fw.FrameworkB, fw.UseCase, intent),
	)

	t4 := createTask(
		fmt.Sprintf("对比 %s 与 %s 的执行结果并生成报告", fw.FrameworkA, fw.FrameworkB),
		"data_agent",
		[]string{t2.ID, t3.ID},
		fmt.Sprintf(`请根据上游 CoderAgent 在沙箱中运行 %s 和 %s 的执行日志与输出结果，生成一份专业的框架对比评测报告。

报告需包含：
1. 实验配置与环境概述
2. %s 运行结果分析
3. %s 运行结果分析
4. 核心指标对比表格（安装耗时、运行耗时、代码复杂度、适用场景）
5. 综合结论与选型建议（针对"%s"场景）

原始需求：%s`, fw.FrameworkA, fw.FrameworkB, fw.FrameworkA, fw.FrameworkB, fw.UseCase, intent),
	)

	plan.Tasks[t1.ID] = t1
	plan.Tasks[t2.ID] = t2
	plan.Tasks[t3.ID] = t3
	plan.Tasks[t4.ID] = t4
}

// buildPaperReproductionPlan 构建论文复现 DAG
func (p *Planner) buildPaperReproductionPlan(plan *models.Plan, intent, paperName string) {
	t1 := createTask(
		fmt.Sprintf("解析论文《%s》并提取核心算法", paperName),
		"librarian_agent",
		nil,
		fmt.Sprintf(`请对论文《%s》进行深度分析，提供：
1. 论文核心创新点与算法原理（通俗解释）
2. 关键模型架构与超参数
3. 推荐的开源代码仓库（GitHub 链接）
4. 复现所需的主要依赖库与版本
5. 常见复现难点与注意事项

原始需求：%s`, paperName, intent),
	)

	t2 := createTask(
		fmt.Sprintf("在沙箱中复现《%s》核心算法", paperName),
		"coder_agent",
		[]string{t1.ID},
		fmt.Sprintf(`请根据上游 LibrarianAgent 提供的论文《%s》分析报告，编写一个完整的 Python 复现脚本。

关键要求：
1. 必须在脚本开头使用 subprocess 安装所有依赖
2. 使用 Dummy 随机数据（无需真实数据集），确保脚本可以独立运行
3. 实现论文的核心算法/模型结构
4. 打印训练/推理过程中的关键指标（Loss、Accuracy 等）
5. 将最终结果以 JSON 格式输出

原始需求：%s`, paperName, intent),
	)

	t3 := createTask(
		fmt.Sprintf("分析《%s》复现结果并生成报告", paperName),
		"data_agent",
		[]string{t2.ID},
		fmt.Sprintf(`请根据上游 CoderAgent 在沙箱中复现论文《%s》的执行日志和输出结果，生成复现报告。

报告需包含：
1. 复现环境说明
2. 算法实现概述
3. 关键指标对比（复现结果 vs 论文声称结果）
4. 遇到的问题与解决方案
5. 结论与改进建议

原始需求：%s`, paperName, intent),
	)

	plan.Tasks[t1.ID] = t1
	plan.Tasks[t2.ID] = t2
	plan.Tasks[t3.ID] = t3
}

// buildCodeExecutionPlan 构建代码执行 DAG
func (p *Planner) buildCodeExecutionPlan(plan *models.Plan, intent string) {
	t1 := createTask(
		"生成并执行 Python 代码",
		"coder_agent",
		nil,
		fmt.Sprintf(`请根据以下需求，编写一个完整可执行的 Python 脚本并在 Docker 沙箱中运行：

需求：%s

关键要求：
1. 必须在脚本开头使用 subprocess 安装所有需要的第三方库
2. 如需绘图，使用 matplotlib 并保存到 /workspace/output_plot.png（禁止调用 plt.show()）
3. 确保脚本独立可运行，不依赖外部文件`, intent),
	)

	t2 := createTask(
		"验证执行结果",
		"data_agent",
		[]string{t1.ID},
		fmt.Sprintf("请分析上游 CoderAgent 的执行结果，验证是否满足用户需求：\n%s\n\n请给出简洁的结果摘要和评估。", intent),
	)

	plan.Tasks[t1.ID] = t1
	plan.Tasks[t2.ID] = t2
}

// enrichTaskDescriptions 使用 LLM 丰富任务描述（可选增强，失败不影响主流程）
func (p *Planner) enrichTaskDescriptions(plan *models.Plan, intent string) {
	if p.llmAPIKey == "" {
		return
	}
	chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: p.llmBaseURL,
		APIKey:  p.llmAPIKey,
		Model:   p.llmModel,
	})
	if err != nil {
		log.Printf("[Planner] 初始化 LLM 失败，跳过描述增强: %v", err)
		return
	}

	// 收集所有任务信息
	tasksSummary := ""
	for _, t := range plan.Tasks {
		tasksSummary += fmt.Sprintf("- [%s] %s (执行者: %s)\n", t.ID[:8], t.Name, t.AssignedTo)
	}

	prompt := fmt.Sprintf(`用户的原始需求是："%s"

系统已生成以下任务计划：
%s

请以 JSON 格式返回一个任务 ID 到补充说明的映射，为每个任务补充 1-2 句话的上下文说明（结合用户的具体需求）。
格式：{"任务ID前8位": "补充说明"}
只返回 JSON，不要其他内容。`, intent, tasksSummary)

	msg, err := chatModel.Generate(context.Background(), []*schema.Message{
		{Role: schema.User, Content: prompt},
	})
	if err != nil {
		log.Printf("[Planner] LLM 描述增强失败: %v", err)
		return
	}

	// 解析 LLM 返回的 JSON
	content := strings.TrimSpace(msg.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var supplements map[string]string
	if err := json.Unmarshal([]byte(content), &supplements); err != nil {
		log.Printf("[Planner] 解析 LLM 补充说明失败: %v", err)
		return
	}

	for id, task := range plan.Tasks {
		shortID := id[:8]
		if supplement, ok := supplements[shortID]; ok {
			task.Description = task.Description + "\n\n[LLM 补充说明] " + supplement
		}
	}
	log.Printf("[Planner] LLM 任务描述增强完成")
}

// createTask 创建一个任务节点
func createTask(name, agent string, deps []string, description string) *models.Task {
	if deps == nil {
		deps = []string{}
	}
	return &models.Task{
		ID:           uuid.New().String(),
		Name:         name,
		Description:  description,
		AssignedTo:   agent,
		Status:       models.StatusPending,
		Dependencies: deps,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}
