import ScholarApp from './app/ScholarApp';
import { AppProviders } from './app/AppProviders';
import { AppErrorBoundary } from './app/AppErrorBoundary';

export default function App() {
  return (
    // 全局渲染异常兜底层
    <AppErrorBoundary>
      {/* 全局上下文/依赖注入层 */}
      <AppProviders>
        {/* 应用主界面 */}
        <ScholarApp />
      </AppProviders>
    </AppErrorBoundary>
  );
}
