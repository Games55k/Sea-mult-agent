import { useCallback, useState } from 'react';
import type { ChatMessage, IntentContext, PlanGraph } from '../../contracts/api';
import { chat, createPlan } from '../../services/api/scholarApi';
import { uiText } from '../constants/uiText';

const isTaskIntent = (input: string) => {
  const normalized = input.toLowerCase();
  return [
    /对比|比较|评估|选型|复现|执行|运行|画图|绘图|代码|论文|报告|总结|分析/,
    /rag|benchmark|plot|matplotlib|python|reproduce|summary|report/,
  ].some((pattern) => pattern.test(normalized));
};

interface UseScholarChatFlowOptions {
  onPlanGraphGenerated: (planGraph: PlanGraph) => void;
}

export function useScholarChatFlow(options: UseScholarChatFlowOptions) {
  const { onPlanGraphGenerated } = options;
  const [prompt, setPrompt] = useState('');
  const [loading, setLoading] = useState(false);
  const [activePlanId, setActivePlanId] = useState<string | null>(null);
  const [intentContext, setIntentContext] = useState<IntentContext | null>(null);
  const [chatHistory, setChatHistory] = useState<ChatMessage[]>([{ role: 'system', text: uiText.appWelcome }]);

  const appendChatMessage = useCallback((message: ChatMessage) => {
    setChatHistory((prev) => [...prev, message]);
  }, []);

  const handleSendMessage = useCallback(async () => {
    if (!prompt.trim()) return;

    const userPrompt = prompt.trim();
    setLoading(true);
    appendChatMessage({ role: 'user', text: userPrompt });
    setPrompt('');

    const isTaskRequest = isTaskIntent(userPrompt);

    try {
      if (isTaskRequest) {
        const response = await createPlan(userPrompt);
        const generatedPlanGraph = response.plan_graph;
        const returnedIntentContext = response.intent_context;
        if (!generatedPlanGraph) throw new Error('Backend did not return plan_graph');

        setIntentContext(returnedIntentContext ?? null);
        setActivePlanId(generatedPlanGraph.id);
        onPlanGraphGenerated(generatedPlanGraph);
        appendChatMessage({
          role: 'system',
          text: uiText.planGenerated,
          actions: ['open_pdf', 'translate_full', 'close_pdf'],
        });
      } else {
        setIntentContext(null);
        const response = await chat(userPrompt);
        appendChatMessage({ role: 'system', text: response.response });
      }
    } catch (error) {
      console.error(error);
      appendChatMessage({ role: 'system', text: uiText.backendError });
    } finally {
      setLoading(false);
    }
  }, [appendChatMessage, onPlanGraphGenerated, prompt]);

  return {
    prompt,
    setPrompt,
    loading,
    chatHistory,
    activePlanId,
    intentContext,
    appendChatMessage,
    handleSendMessage,
  };
}
