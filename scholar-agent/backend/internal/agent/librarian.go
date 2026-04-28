package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/prompts"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// LibrarianAgent 负责文献检索、解析与总结
type LibrarianAgent struct {
	Name         string
	SystemPrompt string
	EinoChain    compose.Runnable[string, string]
}

type librarianContextKey string

const librarianSystemPromptContextKey librarianContextKey = "librarian_system_prompt"

func NewLibrarianAgent() *LibrarianAgent {
	agent := &LibrarianAgent{
		Name:         "librarian_agent",
		SystemPrompt: prompts.LibrarianSystemPrompt,
	}

	agent.initEinoChain()
	return agent
}

func (a *LibrarianAgent) initEinoChain() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is not set")
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	modelName := os.Getenv("OPENAI_MODEL_NAME")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   modelName,
	})
	if err != nil {
		log.Fatalf("初始化文献分析模型失败: %v", err)
	}

	graph := compose.NewGraph[string, string]()

	graph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		logToContext(ctx, "[%s] Eino 节点 [Prompt_Builder]: 正在组装文献分析提示词", a.Name)
		systemPrompt := a.SystemPrompt
		if prompt, ok := ctx.Value(librarianSystemPromptContextKey).(string); ok && prompt != "" {
			systemPrompt = prompt
		}
		messages := []*schema.Message{
			{Role: schema.System, Content: systemPrompt},
			{Role: schema.User, Content: prompts.LibrarianAnalysisUserPrompt(input)},
		}
		return messages, nil
	}))

	// 使用支持流式的 ChatModelNode
	graph.AddChatModelNode("LLM_Analyze_Literature", chatModel)

	graph.AddLambdaNode("Report_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		logToContext(ctx, "[%s] Eino 节点 [Report_Extractor]: 文献分析报告生成完毕", a.Name)
		return msg.Content, nil
	}))

	graph.AddEdge(compose.START, "Prompt_Builder")
	graph.AddEdge("Prompt_Builder", "LLM_Analyze_Literature")
	graph.AddEdge("LLM_Analyze_Literature", "Report_Extractor")
	graph.AddEdge("Report_Extractor", compose.END)

	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Fatalf("编译 Eino 链失败: %v", err)
	}

	a.EinoChain = runnable
}

func (a *LibrarianAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	logToContext(ctx, "[%s] 开始执行任务: %s", a.Name, task.Name)

	input := task.Description
	if task != nil && len(task.Inputs) > 0 {
		input = fmt.Sprintf("%s\n\n上游输入:\n%v", task.Description, task.Inputs)
	}
	intentType := sharedContextValue(sharedContext, "intent_type")
	ctx = context.WithValue(ctx, librarianSystemPromptContextKey, prompts.LibrarianSystemPromptForTask(intentType, task.Type, task.Name, task.Description))

	output, err := a.EinoChain.Invoke(ctx, input)
	if err != nil {
		logToContext(ctx, "[%s] 文献解析失败: %v", a.Name, err)
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("文献解析失败: %v", err)
		return err
	}

	task.Result = output
	task.Status = models.StatusCompleted
	logToContext(ctx, "[%s] 任务完成: %s", a.Name, task.Name)
	return nil
}
