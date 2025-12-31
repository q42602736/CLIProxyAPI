/**
 * OAuth 与设备码登录相关 API
 */

import { apiClient } from './client';

export type OAuthProvider =
  | 'codex'
  | 'anthropic'
  | 'antigravity'
  | 'gemini-cli'
  | 'qwen'
  | 'iflow'
  | 'kiro';

export interface OAuthStartResponse {
  url: string;
  state?: string;
}

export interface OAuthCallbackResponse {
  status: 'ok';
}

export interface IFlowCookieAuthResponse {
  status: 'ok' | 'error';
  error?: string;
  saved_path?: string;
  email?: string;
  expired?: string;
  type?: string;
}

export interface KiroCredentialAuthResponse {
  status: 'ok' | 'error';
  error?: string;
  saved_path?: string;
  region?: string;
  type?: string;
}

export interface KiroImportResponse {
  status: 'ok' | 'error';
  error?: string;
  saved_path?: string;
  region?: string;
  auth_method?: string;
}

export interface KiroFileUploadResponse {
  status: 'ok' | 'error';
  error?: string;
  file_path?: string;
}

export interface KiroUsageLimitsResponse {
  status: 'ok' | 'error';
  error?: string;
  usage?: {
    usageLimitPolicies?: Array<{
      usageLimitResourceType?: string;
      usageLimitValue?: number;
      usageLimitUnit?: string;
      currentValue?: number;
      resetTimestamp?: string;
    }>;
    email?: string;
  };
}

const WEBUI_SUPPORTED: OAuthProvider[] = ['codex', 'anthropic', 'antigravity', 'gemini-cli', 'iflow'];
const CALLBACK_PROVIDER_MAP: Partial<Record<OAuthProvider, string>> = {
  'gemini-cli': 'gemini'
};

export const oauthApi = {
  startAuth: (provider: OAuthProvider, options?: { projectId?: string }) => {
    const params: Record<string, string | boolean> = {};
    if (WEBUI_SUPPORTED.includes(provider)) {
      params.is_webui = true;
    }
    if (provider === 'gemini-cli' && options?.projectId) {
      params.project_id = options.projectId;
    }
    return apiClient.get<OAuthStartResponse>(`/${provider}-auth-url`, {
      params: Object.keys(params).length ? params : undefined
    });
  },

  getAuthStatus: (state: string) =>
    apiClient.get<{ status: 'ok' | 'wait' | 'error'; error?: string }>(`/get-auth-status`, {
      params: { state }
    }),

  submitCallback: (provider: OAuthProvider, redirectUrl: string) => {
    const callbackProvider = CALLBACK_PROVIDER_MAP[provider] ?? provider;
    return apiClient.post<OAuthCallbackResponse>('/oauth-callback', {
      provider: callbackProvider,
      redirect_url: redirectUrl
    });
  },

  /** iFlow cookie 认证 */
  iflowCookieAuth: (cookie: string) =>
    apiClient.post<IFlowCookieAuthResponse>('/iflow-auth-url', { cookie }),

  /** Kiro 凭证加载 */
  kiroCredentialAuth: (credPath?: string) =>
    apiClient.post<KiroCredentialAuthResponse>('/kiro-auth-url', { cred_path: credPath }),

  /** Kiro 文件上传 */
  kiroUploadCredential: async (file: File): Promise<KiroFileUploadResponse> => {
    const formData = new FormData();
    formData.append('file', file);
    formData.append('provider', 'kiro');
    return apiClient.postForm<KiroFileUploadResponse>('/upload-oauth-credentials', formData);
  },

  /** Kiro 用量查询 */
  kiroGetUsageLimits: (authId: string) =>
    apiClient.get<KiroUsageLimitsResponse>('/kiro-usage-limits', { params: { auth_id: authId } }),

  /** Kiro 账号导入 (kiro-account-manager 格式) */
  kiroImportAccount: (data: Record<string, unknown>) =>
    apiClient.post<KiroImportResponse>('/kiro-import', data)
};
