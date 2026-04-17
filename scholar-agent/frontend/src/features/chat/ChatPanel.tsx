import type { ChatMessage } from '../../contracts/api';
import { ChatComposer } from './ChatComposer';
import { ChatMessageList } from './ChatMessageList';

interface ChatPanelState {
  chatHistory: ChatMessage[];
  loading: boolean;
  prompt: string;
  showSuggestions: boolean;
}

interface ChatActions {
  setPrompt: (value: string) => void;
  setShowSuggestions: (next: boolean) => void;
  onSendMessage: () => void;
}

interface PdfActions {
  onOpenPdf: () => void;
  onClosePdf: () => void;
  onFullTranslation: () => void;
}

interface TaskActions {
  onOpenTaskView: (taskId: string, mode: 'plot' | 'report') => void;
}

interface ChatPanelProps {
  state: ChatPanelState;
  chatActions: ChatActions;
  pdfActions: PdfActions;
  taskActions: TaskActions;
}

export function ChatPanel(props: ChatPanelProps) {
  const { state, chatActions, pdfActions, taskActions } = props;

  return (
    <>
      <ChatMessageList
        chatHistory={state.chatHistory}
        loading={state.loading}
        pdfActions={pdfActions}
        taskActions={taskActions}
      />
      <ChatComposer
        prompt={state.prompt}
        loading={state.loading}
        showSuggestions={state.showSuggestions}
        setPrompt={chatActions.setPrompt}
        setShowSuggestions={chatActions.setShowSuggestions}
        onSendMessage={chatActions.onSendMessage}
      />
    </>
  );
}
