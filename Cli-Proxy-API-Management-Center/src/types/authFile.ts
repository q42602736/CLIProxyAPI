/**
 * 认证文件相关类型
 * 基于原项目 src/modules/auth-files.js
 */

export type AuthFileType =
  | 'qwen'
  | 'gemini'
  | 'gemini-cli'
  | 'aistudio'
  | 'claude'
  | 'codex'
  | 'antigravity'
  | 'iflow'
  | 'vertex'
  | 'empty'
  | 'unknown';

export interface AuthFileItem {
  name: string;
  type?: AuthFileType | string;
  provider?: string;
  size?: number;
  authIndex?: string | number | null;
  runtimeOnly?: boolean | string;
  disabled?: boolean;
  modified?: number;
  [key: string]: any;
}

export interface AuthFilesResponse {
  files: AuthFileItem[];
  total?: number;
}

export interface CodexUsageWindow {
  used: number;
  limit: number;
  remaining: number;
  reset_time: string;
  reset_in: number;
}

export interface CodexCredits {
  balance: number;
  has_credits: boolean;
  unlimited: boolean;
}

export interface CodexUsageResponse {
  email: string;
  plan: string;
  session_window: CodexUsageWindow;
  weekly_window: CodexUsageWindow;
  credits: CodexCredits;
  updated_at: string;
}
