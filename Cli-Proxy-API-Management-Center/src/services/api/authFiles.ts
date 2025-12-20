/**
 * 认证文件与 OAuth 排除模型相关 API
 */

import { apiClient } from './client';
import type { AuthFilesResponse } from '@/types/authFile';

export const authFilesApi = {
  list: () => apiClient.get<AuthFilesResponse>('/auth-files'),

  upload: (file: File) => {
    const formData = new FormData();
    formData.append('file', file, file.name);
    return apiClient.postForm('/auth-files', formData);
  },

  deleteFile: (name: string) => apiClient.delete(`/auth-files?name=${encodeURIComponent(name)}`),

  deleteAll: () => apiClient.delete('/auth-files', { params: { all: true } }),

  // 更新认证文件优先级
  updatePriority: (name: string, priority: number) =>
    apiClient.patch(`/auth-files?name=${encodeURIComponent(name)}`, { priority }),

  // 更新认证文件启用/禁用状态
  updateDisabled: (name: string, disabled: boolean) =>
    apiClient.patch(`/auth-files?name=${encodeURIComponent(name)}`, { disabled }),

  // OAuth 排除模型
  async getOauthExcludedModels(): Promise<Record<string, string[]>> {
    const data = await apiClient.get('/oauth-excluded-models');
    const payload = (data && (data['oauth-excluded-models'] ?? data.items ?? data)) as any;
    return payload && typeof payload === 'object' ? payload : {};
  },

  saveOauthExcludedModels: (provider: string, models: string[]) =>
    apiClient.patch('/oauth-excluded-models', { provider, models }),

  deleteOauthExcludedEntry: (provider: string) =>
    apiClient.delete(`/oauth-excluded-models?provider=${encodeURIComponent(provider)}`),

  // 获取认证凭证支持的模型
  async getModelsForAuthFile(name: string): Promise<{ id: string; display_name?: string; type?: string; owned_by?: string }[]> {
    const data = await apiClient.get(`/auth-files/models?name=${encodeURIComponent(name)}`);
    return (data && Array.isArray(data['models'])) ? data['models'] : [];
  },

  // 获取 Antigravity 认证文件的配额信息
  async getAntigravityQuotas(): Promise<Record<string, Record<string, { remaining: number; resetTime: string }>>> {
    const data = await apiClient.get('/antigravity-quotas');
    return data?.quotas || {};
  },

  // 获取 Kiro 认证文件的用量信息
  async getKiroUsageLimits(authId: string): Promise<{
    usageBreakdownList?: Array<{
      resourceType?: string;
      displayName?: string;
      unit?: string;
      currentUsage?: number;
      currentUsageWithPrecision?: number;
      usageLimit?: number;
      usageLimitWithPrecision?: number;
      nextDateReset?: number;
      freeTrialInfo?: {
        freeTrialStatus?: string;
        currentUsage?: number;
        currentUsageWithPrecision?: number;
        usageLimit?: number;
        usageLimitWithPrecision?: number;
        freeTrialExpiry?: number;
      };
      bonuses?: Array<{
        bonusCode?: string;
        displayName?: string;
        description?: string;
        status?: string;
        currentUsage?: number;
        usageLimit?: number;
        redeemedAt?: number;
        expiresAt?: number;
      }>;
    }>;
    userInfo?: {
      email?: string;
      userId?: string;
    };
    daysUntilReset?: number;
    nextDateReset?: number;
  } | null> {
    try {
      const data = await apiClient.get('/kiro-usage-limits', { params: { auth_id: authId } });
      return data?.usage || null;
    } catch {
      return null;
    }
  }
};
