const resolveDefaultApiBaseUrl = () => {
  // 优先允许通过环境变量显式指定，便于部署或联调时覆盖。
  const envApiBaseUrl = import.meta.env.VITE_API_BASE_URL as string | undefined;
  if (envApiBaseUrl) {
    return envApiBaseUrl;
  }

  // 开发态默认跟随当前页面主机，避免通过局域网地址访问前端时仍错误请求 localhost。
  if (typeof window !== 'undefined') {
    const { hostname } = window.location;
    const apiHost = hostname || 'localhost';
    return `http://${apiHost}:8080`;
  }

  return 'http://localhost:8080';
};

export const API_BASE_URL = resolveDefaultApiBaseUrl();
