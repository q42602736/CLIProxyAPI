import { useEffect, useMemo, useRef, useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { Button } from '@/components/ui/Button';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { EmptyState } from '@/components/ui/EmptyState';
import { IconBot, IconDownload, IconInfo, IconTrash2 } from '@/components/ui/icons';
import { useAuthStore, useNotificationStore, useThemeStore } from '@/stores';
import { authFilesApi, usageApi } from '@/services/api';
import { apiClient } from '@/services/api/client';
import type { AuthFileItem } from '@/types';
import type { KeyStats, KeyStatBucket } from '@/utils/usage';
import { formatFileSize } from '@/utils/format';
import styles from './AuthFilesPage.module.scss';

type ThemeColors = { bg: string; text: string; border?: string };
type TypeColorSet = { light: ThemeColors; dark?: ThemeColors };

// 标签类型颜色配置（对齐重构前 styles.css 的 file-type-badge 颜色）
const TYPE_COLORS: Record<string, TypeColorSet> = {
  qwen: {
    light: { bg: '#e8f5e9', text: '#2e7d32' },
    dark: { bg: '#1b5e20', text: '#81c784' }
  },
  gemini: {
    light: { bg: '#e3f2fd', text: '#1565c0' },
    dark: { bg: '#0d47a1', text: '#64b5f6' }
  },
  'gemini-cli': {
    light: { bg: '#e7efff', text: '#1e4fa3' },
    dark: { bg: '#1c3f73', text: '#a8c7ff' }
  },
  aistudio: {
    light: { bg: '#f0f2f5', text: '#2f343c' },
    dark: { bg: '#373c42', text: '#cfd3db' }
  },
  claude: {
    light: { bg: '#fce4ec', text: '#c2185b' },
    dark: { bg: '#880e4f', text: '#f48fb1' }
  },
  codex: {
    light: { bg: '#fff3e0', text: '#ef6c00' },
    dark: { bg: '#e65100', text: '#ffb74d' }
  },
  antigravity: {
    light: { bg: '#e0f7fa', text: '#006064' },
    dark: { bg: '#004d40', text: '#80deea' }
  },
  iflow: {
    light: { bg: '#f3e5f5', text: '#7b1fa2' },
    dark: { bg: '#4a148c', text: '#ce93d8' }
  },
  kiro: {
    light: { bg: '#fff8e1', text: '#ff8f00' },
    dark: { bg: '#ff6f00', text: '#ffe082' }
  },
  empty: {
    light: { bg: '#f5f5f5', text: '#616161' },
    dark: { bg: '#424242', text: '#bdbdbd' }
  },
  unknown: {
    light: { bg: '#f0f0f0', text: '#666666', border: '1px dashed #999999' },
    dark: { bg: '#3a3a3a', text: '#aaaaaa', border: '1px dashed #666666' }
  }
};

interface ExcludedFormState {
  provider: string;
  modelsText: string;
}

// 标准化 auth_index 值（与 usage.ts 中的 normalizeAuthIndex 保持一致）
function normalizeAuthIndexValue(value: unknown): string | null {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value.toString();
  }
  if (typeof value === 'string') {
    const trimmed = value.trim();
    return trimmed ? trimmed : null;
  }
  return null;
}

function isRuntimeOnlyAuthFile(file: AuthFileItem): boolean {
  const raw = file['runtime_only'] ?? file.runtimeOnly;
  if (typeof raw === 'boolean') return raw;
  if (typeof raw === 'string') return raw.trim().toLowerCase() === 'true';
  return false;
}

// 解析认证文件的统计数据
function resolveAuthFileStats(
  file: AuthFileItem,
  stats: KeyStats
): KeyStatBucket {
  const defaultStats: KeyStatBucket = { success: 0, failure: 0 };
  const rawFileName = file?.name || '';

  // 兼容 auth_index 和 authIndex 两种字段名（API 返回的是 auth_index）
  const rawAuthIndex = file['auth_index'] ?? file.authIndex;
  const authIndexKey = normalizeAuthIndexValue(rawAuthIndex);

  // 尝试根据 authIndex 匹配
  if (authIndexKey && stats.byAuthIndex?.[authIndexKey]) {
    return stats.byAuthIndex[authIndexKey];
  }

  // 尝试根据 source (文件名) 匹配
  if (rawFileName && stats.bySource?.[rawFileName]) {
    const fromName = stats.bySource[rawFileName];
    if (fromName.success > 0 || fromName.failure > 0) {
      return fromName;
    }
  }

  // 尝试去掉扩展名后匹配
  if (rawFileName) {
    const nameWithoutExt = rawFileName.replace(/\.[^/.]+$/, '');
    if (nameWithoutExt && nameWithoutExt !== rawFileName) {
      const fromNameWithoutExt = stats.bySource?.[nameWithoutExt];
      if (fromNameWithoutExt && (fromNameWithoutExt.success > 0 || fromNameWithoutExt.failure > 0)) {
        return fromNameWithoutExt;
      }
    }
  }

  return defaultStats;
}

// 格式化配额重置倒计时
const formatTimeUntilReset = (resetTime: string, now: Date): string => {
  if (!resetTime) return '';

  const resetDate = new Date(resetTime);
  const ms = resetDate.getTime() - now.getTime();

  if (ms <= 0) {
    return '已过期';
  }

  const seconds = Math.floor(ms / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);

  if (days > 0) {
    return `${days}天${hours % 24}小时后重置`;
  } else if (hours > 0) {
    return `${hours}小时${minutes % 60}分钟后重置`;
  } else if (minutes > 0) {
    return `${minutes}分${seconds % 60}秒后重置`;
  }
  return `${seconds}秒后重置`;
};

export function AuthFilesPage() {
  const { t } = useTranslation();
  const { showNotification } = useNotificationStore();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const theme = useThemeStore((state) => state.theme);

  const [files, setFiles] = useState<AuthFileItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [filter, setFilter] = useState<'all' | string>('all');
  const [search, setSearch] = useState('');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(9);
  const [uploading, setUploading] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);
  const [deletingAll, setDeletingAll] = useState(false);
  const [keyStats, setKeyStats] = useState<KeyStats>({ bySource: {}, byAuthIndex: {} });
  const [antigravityQuotas, setAntigravityQuotas] = useState<Record<string, Record<string, { remaining: number; resetTime: string }>>>({});
  const [kiroUsageLimits, setKiroUsageLimits] = useState<Record<string, {
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
  }>>({});
  const [quotaRefreshInterval, setQuotaRefreshInterval] = useState(() => {
    // 从 localStorage 读取保存的配额刷新间隔，默认1分钟
    const saved = localStorage.getItem('quotaRefreshInterval');
    return saved ? parseInt(saved) : 1;
  });
  const [currentTime, setCurrentTime] = useState(new Date()); // 用于实时更新倒计时
  const [priorityInputs, setPriorityInputs] = useState<Record<string, number>>({}); // 临时存储优先级输入值

  // 详情弹窗相关
  const [detailModalOpen, setDetailModalOpen] = useState(false);
  const [selectedFile, setSelectedFile] = useState<AuthFileItem | null>(null);

  // 模型列表弹窗相关
  const [modelsModalOpen, setModelsModalOpen] = useState(false);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsList, setModelsList] = useState<{ id: string; display_name?: string; type?: string }[]>([]);
  const [modelsFileName, setModelsFileName] = useState('');
  const [modelsFileType, setModelsFileType] = useState('');
  const [modelsError, setModelsError] = useState<'unsupported' | null>(null);

  // OAuth 排除模型相关
  const [excluded, setExcluded] = useState<Record<string, string[]>>({});
  const [excludedError, setExcludedError] = useState<'unsupported' | null>(null);
  const [excludedModalOpen, setExcludedModalOpen] = useState(false);
  const [excludedForm, setExcludedForm] = useState<ExcludedFormState>({ provider: '', modelsText: '' });
  const [savingExcluded, setSavingExcluded] = useState(false);

  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const excludedUnsupportedRef = useRef(false);

  const disableControls = connectionStatus !== 'connected';

  // 格式化修改时间
  const formatModified = (item: AuthFileItem): string => {
    const raw = item['modtime'] ?? item.modified;
    if (!raw) return '-';
    const asNumber = Number(raw);
    const date =
      Number.isFinite(asNumber) && !Number.isNaN(asNumber)
        ? new Date(asNumber < 1e12 ? asNumber * 1000 : asNumber)
        : new Date(String(raw));
    return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString();
  };

  // 加载文件列表
  const loadFiles = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await authFilesApi.list();
      setFiles(data?.files || []);
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : t('notification.refresh_failed');
      setError(errorMessage);
    } finally {
      setLoading(false);
    }
  }, [t]);

  // 加载 key 统计
  const loadKeyStats = useCallback(async () => {
    try {
      const stats = await usageApi.getKeyStats();
      setKeyStats(stats);
    } catch {
      // 静默失败
    }
  }, []);

  // 加载 Antigravity 配额
  const loadAntigravityQuotas = useCallback(async () => {
    try {
      const quotas = await authFilesApi.getAntigravityQuotas();
      setAntigravityQuotas(quotas);
    } catch {
      // 静默失败
    }
  }, []);

  // 加载 Kiro 用量限制
  const loadKiroUsageLimits = useCallback(async (fileList: AuthFileItem[]) => {
    const kiroFiles = fileList.filter((f) => f.type === 'kiro');
    if (kiroFiles.length === 0) return;

    const newLimits: Record<string, typeof kiroUsageLimits[string]> = {};
    for (const file of kiroFiles) {
      try {
        const usage = await authFilesApi.getKiroUsageLimits(file.name);
        if (usage) {
          newLimits[file.name] = usage;
        }
      } catch {
        // 静默失败
      }
    }
    setKiroUsageLimits(newLimits);
  }, []);

  // 加载 OAuth 排除列表
  const loadExcluded = useCallback(async () => {
    try {
      const res = await authFilesApi.getOauthExcludedModels();
      excludedUnsupportedRef.current = false;
      setExcluded(res || {});
      setExcludedError(null);
    } catch (err: unknown) {
      const status =
        typeof err === 'object' && err !== null && 'status' in err
          ? (err as { status?: unknown }).status
          : undefined;

      if (status === 404) {
        setExcluded({});
        setExcludedError('unsupported');
        if (!excludedUnsupportedRef.current) {
          excludedUnsupportedRef.current = true;
          showNotification(t('oauth_excluded.upgrade_required'), 'warning');
        }
        return;
      }
      // 静默失败
    }
  }, [showNotification, t]);

  // 保存配额刷新间隔到 localStorage
  useEffect(() => {
    localStorage.setItem('quotaRefreshInterval', quotaRefreshInterval.toString());
  }, [quotaRefreshInterval]);

  useEffect(() => {
    loadFiles();
    loadKeyStats();
    loadAntigravityQuotas();
    loadExcluded();

    // 设置自动刷新配额信息（使用可配置的间隔时间，单位：分钟）
    const quotaRefreshIntervalId = setInterval(() => {
      loadAntigravityQuotas();
      // 同时刷新 Kiro 用量（需要最新的文件列表）
      authFilesApi.list().then((data) => {
        if (data?.files) {
          loadKiroUsageLimits(data.files);
        }
      }).catch(() => {});
    }, quotaRefreshInterval * 60 * 1000); // 转换为毫秒

    // 设置每秒更新当前时间，用于实时刷新倒计时显示
    const timeUpdateIntervalId = setInterval(() => {
      setCurrentTime(new Date());
    }, 1000);

    // 清理定时器
    return () => {
      clearInterval(quotaRefreshIntervalId);
      clearInterval(timeUpdateIntervalId);
    };
  }, [loadFiles, loadKeyStats, loadExcluded, loadAntigravityQuotas, quotaRefreshInterval]);

  // 当文件列表变化时加载 Kiro 用量
  useEffect(() => {
    if (files.length > 0) {
      loadKiroUsageLimits(files);
    }
  }, [files, loadKiroUsageLimits]);

  // 提取所有存在的类型
  const existingTypes = useMemo(() => {
    const types = new Set<string>(['all']);
    files.forEach((file) => {
      if (file.type) {
        types.add(file.type);
      }
    });
    return Array.from(types);
  }, [files]);

  // 过滤和搜索
  const filtered = useMemo(() => {
    return files.filter((item) => {
      const matchType = filter === 'all' || item.type === filter;
      const term = search.trim().toLowerCase();
      const matchSearch =
        !term ||
        item.name.toLowerCase().includes(term) ||
        (item.type || '').toString().toLowerCase().includes(term) ||
        (item.provider || '').toString().toLowerCase().includes(term);
      return matchType && matchSearch;
    });
  }, [files, filter, search]);

  // 分页计算
  const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const currentPage = Math.min(page, totalPages);
  const start = (currentPage - 1) * pageSize;
  const pageItems = filtered.slice(start, start + pageSize);

  // 统计信息
  const totalSize = useMemo(() => files.reduce((sum, item) => sum + (item.size || 0), 0), [files]);

  // 点击上传
  const handleUploadClick = () => {
    fileInputRef.current?.click();
  };

  // 处理文件上传（支持多选）
  const handleFileChange = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const fileList = event.target.files;
    if (!fileList || fileList.length === 0) return;

    const filesToUpload = Array.from(fileList);
    const validFiles: File[] = [];
    const invalidFiles: string[] = [];

    filesToUpload.forEach((file) => {
      if (file.name.endsWith('.json')) {
        validFiles.push(file);
      } else {
        invalidFiles.push(file.name);
      }
    });

    if (invalidFiles.length > 0) {
      showNotification(t('auth_files.upload_error_json'), 'error');
    }

    if (validFiles.length === 0) {
      event.target.value = '';
      return;
    }

    setUploading(true);
    let successCount = 0;
    const failed: { name: string; message: string }[] = [];

    for (const file of validFiles) {
      try {
        await authFilesApi.upload(file);
        successCount++;
      } catch (err: unknown) {
        const errorMessage = err instanceof Error ? err.message : 'Unknown error';
        failed.push({ name: file.name, message: errorMessage });
      }
    }

    if (successCount > 0) {
      const suffix = validFiles.length > 1 ? ` (${successCount}/${validFiles.length})` : '';
      showNotification(`${t('auth_files.upload_success')}${suffix}`, failed.length ? 'warning' : 'success');
      await loadFiles();
      await loadKeyStats();
    }

    if (failed.length > 0) {
      const details = failed.map((item) => `${item.name}: ${item.message}`).join('; ');
      showNotification(`${t('notification.upload_failed')}: ${details}`, 'error');
    }

    setUploading(false);
    event.target.value = '';
  };

  // 更新优先级
  const handlePriorityChange = async (name: string, priority: number) => {
    try {
      await authFilesApi.updatePriority(name, priority);
      // 更新本地状态
      setFiles((prev) =>
        prev.map((file) =>
          file.name === name ? { ...file, priority } : file
        )
      );
      // 清除临时输入值
      setPriorityInputs((prev) => {
        const newInputs = { ...prev };
        delete newInputs[name];
        return newInputs;
      });
      showNotification(t('auth_files.priority_update_success', { defaultValue: '优先级更新成功' }), 'success');
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : '';
      showNotification(`${t('auth_files.priority_update_failed', { defaultValue: '优先级更新失败' })}: ${errorMessage}`, 'error');
    }
  };

  // 获取优先级显示值（优先使用临时输入值）
  const getPriorityValue = (name: string, filePriority: number) => {
    return priorityInputs[name] !== undefined ? priorityInputs[name] : (filePriority || 0);
  };

  // 删除单个文件
  const handleDelete = async (name: string) => {
    if (!window.confirm(`${t('auth_files.delete_confirm')} "${name}" ?`)) return;
    setDeleting(name);
    try {
      await authFilesApi.deleteFile(name);
      showNotification(t('auth_files.delete_success'), 'success');
      setFiles((prev) => prev.filter((item) => item.name !== name));
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : '';
      showNotification(`${t('notification.delete_failed')}: ${errorMessage}`, 'error');
    } finally {
      setDeleting(null);
    }
  };

  // 删除全部（根据筛选类型）
  const handleDeleteAll = async () => {
    const isFiltered = filter !== 'all';
    const typeLabel = isFiltered ? getTypeLabel(filter) : t('auth_files.filter_all');
    const confirmMessage = isFiltered
      ? t('auth_files.delete_filtered_confirm', { type: typeLabel })
      : t('auth_files.delete_all_confirm');

    if (!window.confirm(confirmMessage)) return;

    setDeletingAll(true);
    try {
      if (!isFiltered) {
        // 删除全部
        await authFilesApi.deleteAll();
        showNotification(t('auth_files.delete_all_success'), 'success');
        setFiles((prev) => prev.filter((file) => isRuntimeOnlyAuthFile(file)));
      } else {
        // 删除筛选类型的文件
        const filesToDelete = files.filter(
          (f) => f.type === filter && !isRuntimeOnlyAuthFile(f)
        );

        if (filesToDelete.length === 0) {
          showNotification(t('auth_files.delete_filtered_none', { type: typeLabel }), 'info');
          setDeletingAll(false);
          return;
        }

        let success = 0;
        let failed = 0;
        const deletedNames: string[] = [];

        for (const file of filesToDelete) {
          try {
            await authFilesApi.deleteFile(file.name);
            success++;
            deletedNames.push(file.name);
          } catch {
            failed++;
          }
        }

        setFiles((prev) => prev.filter((f) => !deletedNames.includes(f.name)));

        if (failed === 0) {
          showNotification(
            t('auth_files.delete_filtered_success', { count: success, type: typeLabel }),
            'success'
          );
        } else {
          showNotification(
            t('auth_files.delete_filtered_partial', { success, failed, type: typeLabel }),
            'warning'
          );
        }
        setFilter('all');
      }
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : '';
      showNotification(`${t('notification.delete_failed')}: ${errorMessage}`, 'error');
    } finally {
      setDeletingAll(false);
    }
  };

  // 下载文件
  const handleDownload = async (name: string) => {
    try {
      const response = await apiClient.getRaw(`/auth-files/download?name=${encodeURIComponent(name)}`, {
        responseType: 'blob'
      });
      const blob = new Blob([response.data]);
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = name;
      a.click();
      window.URL.revokeObjectURL(url);
      showNotification(t('auth_files.download_success'), 'success');
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : '';
      showNotification(`${t('notification.download_failed')}: ${errorMessage}`, 'error');
    }
  };

  // 显示详情弹窗
  const showDetails = (file: AuthFileItem) => {
    setSelectedFile(file);
    setDetailModalOpen(true);
  };

  // 显示模型列表
  const showModels = async (item: AuthFileItem) => {
    setModelsFileName(item.name);
    setModelsFileType(item.type || '');
    setModelsList([]);
    setModelsError(null);
    setModelsModalOpen(true);
    setModelsLoading(true);
    try {
      const models = await authFilesApi.getModelsForAuthFile(item.name);
      setModelsList(models);
    } catch (err) {
      // 检测是否是 API 不支持的错误 (404 或特定错误消息)
      const errorMessage = err instanceof Error ? err.message : '';
      if (errorMessage.includes('404') || errorMessage.includes('not found') || errorMessage.includes('Not Found')) {
        setModelsError('unsupported');
      } else {
        showNotification(`${t('notification.load_failed')}: ${errorMessage}`, 'error');
      }
    } finally {
      setModelsLoading(false);
    }
  };

  // 检查模型是否被 OAuth 排除
  const isModelExcluded = (modelId: string, providerType: string): boolean => {
    const excludedModels = excluded[providerType] || [];
    return excludedModels.some(pattern => {
      if (pattern.includes('*')) {
        // 支持通配符匹配
        const regex = new RegExp('^' + pattern.replace(/\*/g, '.*') + '$', 'i');
        return regex.test(modelId);
      }
      return pattern.toLowerCase() === modelId.toLowerCase();
    });
  };

  // 获取类型标签显示文本
  const getTypeLabel = (type: string): string => {
    const key = `auth_files.filter_${type}`;
    const translated = t(key);
    if (translated !== key) return translated;
    if (type.toLowerCase() === 'iflow') return 'iFlow';
    return type.charAt(0).toUpperCase() + type.slice(1);
  };

  // 获取类型颜色
  const getTypeColor = (type: string): ThemeColors => {
    const set = TYPE_COLORS[type] || TYPE_COLORS.unknown;
    return theme === 'dark' && set.dark ? set.dark : set.light;
  };

  // OAuth 排除相关方法
  const openExcludedModal = (provider?: string) => {
    const models = provider ? excluded[provider] : [];
    setExcludedForm({
      provider: provider || '',
      modelsText: Array.isArray(models) ? models.join('\n') : ''
    });
    setExcludedModalOpen(true);
  };

  const saveExcludedModels = async () => {
    const provider = excludedForm.provider.trim();
    if (!provider) {
      showNotification(t('oauth_excluded.provider_required'), 'error');
      return;
    }
    const models = excludedForm.modelsText
      .split(/[\n,]+/)
      .map((item) => item.trim())
      .filter(Boolean);
    setSavingExcluded(true);
    try {
      if (models.length) {
        await authFilesApi.saveOauthExcludedModels(provider, models);
      } else {
        await authFilesApi.deleteOauthExcludedEntry(provider);
      }
      await loadExcluded();
      showNotification(t('oauth_excluded.save_success'), 'success');
      setExcludedModalOpen(false);
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : '';
      showNotification(`${t('oauth_excluded.save_failed')}: ${errorMessage}`, 'error');
    } finally {
      setSavingExcluded(false);
    }
  };

  const deleteExcluded = async (provider: string) => {
    if (!window.confirm(t('oauth_excluded.delete_confirm', { provider }))) return;
    try {
      await authFilesApi.deleteOauthExcludedEntry(provider);
      await loadExcluded();
      showNotification(t('oauth_excluded.delete_success'), 'success');
    } catch (err: unknown) {
      const errorMessage = err instanceof Error ? err.message : '';
      showNotification(`${t('oauth_excluded.delete_failed')}: ${errorMessage}`, 'error');
    }
  };

  // 渲染标签筛选器
  const renderFilterTags = () => (
    <div className={styles.filterTags}>
      {existingTypes.map((type) => {
        const isActive = filter === type;
        const color = type === 'all' ? { bg: 'var(--bg-tertiary)', text: 'var(--text-primary)' } : getTypeColor(type);
        const activeTextColor = theme === 'dark' ? '#111827' : '#fff';
        return (
          <button
            key={type}
            className={`${styles.filterTag} ${isActive ? styles.filterTagActive : ''}`}
            style={{
              backgroundColor: isActive ? color.text : color.bg,
              color: isActive ? activeTextColor : color.text,
              borderColor: color.text
            }}
            onClick={() => {
              setFilter(type);
              setPage(1);
            }}
          >
            {getTypeLabel(type)}
          </button>
        );
      })}
    </div>
  );

  // 渲染单个认证文件卡片
  const renderFileCard = (item: AuthFileItem) => {
      const fileStats = resolveAuthFileStats(item, keyStats);
    const isRuntimeOnly = isRuntimeOnlyAuthFile(item);
    const typeColor = getTypeColor(item.type || 'unknown');

    return (
      <div key={item.name} className={styles.fileCard}>
        <div className={styles.cardHeader}>
          <span
            className={styles.typeBadge}
            style={{
              backgroundColor: typeColor.bg,
              color: typeColor.text,
              ...(typeColor.border ? { border: typeColor.border } : {})
            }}
          >
            {getTypeLabel(item.type || 'unknown')}
          </span>
          <span className={styles.fileName}>{item.name}</span>
        </div>

        <div className={styles.cardMeta}>
          <span>{t('auth_files.file_size')}: {item.size ? formatFileSize(item.size) : '-'}</span>
          <span>{t('auth_files.file_modified')}: {formatModified(item)}</span>
          <span className={styles.priorityWrapper}>
            {t('auth_files.priority', { defaultValue: '优先级' })}:
            <input
              type="number"
              value={getPriorityValue(item.name, item.priority)}
              onChange={(e) => setPriorityInputs((prev) => ({ ...prev, [item.name]: parseInt(e.target.value) || 0 }))}
              className={styles.priorityInput}
              disabled={disableControls}
              title={t('auth_files.priority_hint', { defaultValue: '数值越大优先级越高' })}
            />
            <button
              className={styles.priorityConfirmBtn}
              onClick={() => handlePriorityChange(item.name, getPriorityValue(item.name, item.priority))}
              disabled={disableControls || priorityInputs[item.name] === undefined}
              title="确认"
            >
              ✓
            </button>
          </span>
        </div>

        <div className={styles.cardStats}>
          <span className={`${styles.statPill} ${styles.statSuccess}`}>
            {t('stats.success')}: {fileStats.success}
          </span>
          <span className={`${styles.statPill} ${styles.statFailure}`}>
            {t('stats.failure')}: {fileStats.failure}
          </span>
        </div>

        {/* Antigravity 余额显示 */}
        {item.type === 'antigravity' && antigravityQuotas[item.name] && (
          <div className={styles.quotaInfo}>
            <div className={styles.quotaTitle}>
              模型配额 ({Object.keys(antigravityQuotas[item.name]).length} 个模型)
            </div>
            {Object.entries(antigravityQuotas[item.name]).map(([modelId, quota]) => {
              const remainingPercent = quota.remaining * 100;
              const progressClass = remainingPercent >= 70 ? styles.progressNormal : (remainingPercent >= 30 ? styles.progressWarning : styles.progressDanger);
              const resetCountdown = formatTimeUntilReset(quota.resetTime, currentTime);

              return (
                <div key={modelId} className={styles.quotaItem}>
                  <div className={styles.quotaHeader}>
                    <span className={styles.quotaModel}>{modelId}</span>
                    <span className={styles.quotaValue}>
                      剩余: {remainingPercent.toFixed(1)}%
                    </span>
                  </div>
                  <div className={`${styles.progressBar} ${progressClass}`}>
                    <div className={styles.progressFill} style={{ width: `${remainingPercent}%` }}></div>
                  </div>
                  {resetCountdown && (
                    <div className={styles.quotaReset}>
                      {resetCountdown}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}

        {/* Kiro 用量显示 */}
        {item.type === 'kiro' && kiroUsageLimits[item.name] && (
          <div className={styles.quotaInfo}>
            <div className={styles.quotaTitle}>
              用量限制 {kiroUsageLimits[item.name].userInfo?.email && `(${kiroUsageLimits[item.name].userInfo?.email})`}
            </div>
            {kiroUsageLimits[item.name].usageBreakdownList?.map((breakdown, idx) => {
              // 基础额度
              const baseUsed = breakdown.currentUsageWithPrecision ?? breakdown.currentUsage ?? 0;
              const baseTotal = breakdown.usageLimitWithPrecision ?? breakdown.usageLimit ?? 0;
              // 免费试用额度
              const trialUsed = breakdown.freeTrialInfo?.currentUsageWithPrecision ?? breakdown.freeTrialInfo?.currentUsage ?? 0;
              const trialTotal = breakdown.freeTrialInfo?.usageLimitWithPrecision ?? breakdown.freeTrialInfo?.usageLimit ?? 0;
              // 总额度 = 基础 + 免费试用
              const totalUsed = baseUsed + trialUsed;
              const totalLimit = baseTotal + trialTotal;
              const totalUsedPercent = totalLimit > 0 ? Math.min((totalUsed / totalLimit) * 100, 100) : 0;
              const totalRemainingPercent = 100 - totalUsedPercent;
              const totalProgressClass = totalRemainingPercent >= 70 ? styles.progressNormal : (totalRemainingPercent >= 30 ? styles.progressWarning : styles.progressDanger);
              const resetTime = breakdown.nextDateReset ? new Date(breakdown.nextDateReset * 1000).toISOString() : null;
              const resetCountdown = resetTime ? formatTimeUntilReset(resetTime, currentTime) : null;

              return (
                <div key={idx} className={styles.quotaItem}>
                  {/* 总额度显示 */}
                  <div className={styles.quotaHeader}>
                    <span className={styles.quotaModel}>📊 {breakdown.displayName || breakdown.resourceType || 'Usage'} (总计)</span>
                    <span className={styles.quotaValue}>
                      {totalUsed.toFixed(2)}/{totalLimit}
                    </span>
                  </div>
                  <div className={`${styles.progressBar} ${totalProgressClass}`}>
                    <div className={styles.progressFill} style={{ width: `${totalRemainingPercent}%` }}></div>
                  </div>
                  {resetCountdown && (
                    <div className={styles.quotaReset}>
                      {resetCountdown}
                    </div>
                  )}
                  {/* 基础额度明细 */}
                  {baseTotal > 0 && (
                    <div className={styles.quotaItem} style={{ marginTop: '8px', paddingLeft: '12px', borderLeft: '2px solid #1890ff' }}>
                      <div className={styles.quotaHeader}>
                        <span className={styles.quotaModel} style={{ color: '#1890ff' }}>💎 基础额度</span>
                        <span className={styles.quotaValue}>
                          {baseUsed}/{baseTotal}
                        </span>
                      </div>
                    </div>
                  )}
                  {/* 免费试用额度明细 */}
                  {breakdown.freeTrialInfo && breakdown.freeTrialInfo.freeTrialStatus === 'ACTIVE' && (() => {
                    const trialExpiry = breakdown.freeTrialInfo.freeTrialExpiry ? formatTimeUntilReset(new Date(breakdown.freeTrialInfo.freeTrialExpiry * 1000).toISOString(), currentTime) : null;
                    return (
                      <div className={styles.quotaItem} style={{ marginTop: '8px', paddingLeft: '12px', borderLeft: '2px solid #52c41a' }}>
                        <div className={styles.quotaHeader}>
                          <span className={styles.quotaModel} style={{ color: '#52c41a' }}>🎁 免费试用额度</span>
                          <span className={styles.quotaValue}>
                            {trialUsed.toFixed(2)}/{trialTotal}
                          </span>
                        </div>
                        {trialExpiry && (
                          <div className={styles.quotaReset} style={{ fontSize: '11px', color: '#888' }}>
                            试用到期: {trialExpiry}
                          </div>
                        )}
                      </div>
                    );
                  })()}
                  {/* 显示奖励额度 */}
                  {breakdown.bonuses && breakdown.bonuses.length > 0 && breakdown.bonuses.map((bonus, bonusIdx) => {
                    const bonusUsed = bonus.currentUsage ?? 0;
                    const bonusTotal = bonus.usageLimit ?? 1;
                    const bonusUsedPercent = Math.min((bonusUsed / bonusTotal) * 100, 100);
                    const bonusRemainingPercent = 100 - bonusUsedPercent;
                    const bonusProgressClass = bonusRemainingPercent >= 70 ? styles.progressNormal : (bonusRemainingPercent >= 30 ? styles.progressWarning : styles.progressDanger);
                    return (
                      <div key={`bonus-${bonusIdx}`} className={styles.quotaItem} style={{ marginTop: '8px', paddingLeft: '12px', borderLeft: '2px solid #faad14' }}>
                        <div className={styles.quotaHeader}>
                          <span className={styles.quotaModel} style={{ color: '#faad14' }}>🎁 {bonus.displayName || bonus.bonusCode || '赠送额度'}</span>
                          <span className={styles.quotaValue}>
                            {bonusUsed}/{bonusTotal}
                          </span>
                        </div>
                        <div className={`${styles.progressBar} ${bonusProgressClass}`}>
                          <div className={styles.progressFill} style={{ width: `${bonusRemainingPercent}%` }}></div>
                        </div>
                        {bonus.description && (
                          <div className={styles.quotaReset} style={{ fontSize: '11px', color: '#888' }}>
                            {bonus.description}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              );
            })}
          </div>
        )}

        <div className={styles.cardActions}>
          {isRuntimeOnly ? (
            <div className={styles.virtualBadge}>{t('auth_files.type_virtual') || '虚拟认证文件'}</div>
          ) : (
            <>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => showModels(item)}
                className={styles.iconButton}
                title={t('auth_files.models_button', { defaultValue: '模型' })}
                disabled={disableControls}
              >
                <IconBot className={styles.actionIcon} size={16} />
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => showDetails(item)}
                className={styles.iconButton}
                title={t('common.info', { defaultValue: '关于' })}
                disabled={disableControls}
              >
                <IconInfo className={styles.actionIcon} size={16} />
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => handleDownload(item.name)}
                className={styles.iconButton}
                title={t('auth_files.download_button')}
                disabled={disableControls}
              >
                <IconDownload className={styles.actionIcon} size={16} />
              </Button>
              <Button
                variant="danger"
                size="sm"
                onClick={() => handleDelete(item.name)}
                className={styles.iconButton}
                title={t('auth_files.delete_button')}
                disabled={disableControls || deleting === item.name}
              >
                {deleting === item.name ? (
                  <LoadingSpinner size={14} />
                ) : (
                  <IconTrash2 className={styles.actionIcon} size={16} />
                )}
              </Button>
            </>
          )}
        </div>
      </div>
    );
  };

  return (
    <div className={styles.container}>
      <div className={styles.pageHeader}>
        <h1 className={styles.pageTitle}>{t('auth_files.title')}</h1>
        <p className={styles.description}>{t('auth_files.description')}</p>
      </div>

      <Card
        title={t('auth_files.title_section')}
        extra={
          <div className={styles.headerActions}>
            <Button variant="secondary" size="sm" onClick={() => { loadFiles(); loadKeyStats(); loadAntigravityQuotas(); }} disabled={loading}>
              {t('common.refresh')}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={handleDeleteAll}
              disabled={disableControls || loading || deletingAll}
              loading={deletingAll}
            >
              {filter === 'all' ? t('auth_files.delete_all_button') : `${t('common.delete')} ${getTypeLabel(filter)}`}
            </Button>
            <Button size="sm" onClick={handleUploadClick} disabled={disableControls || uploading} loading={uploading}>
              {t('auth_files.upload_button')}
            </Button>
            <input
              ref={fileInputRef}
              type="file"
              accept=".json,application/json"
              multiple
              style={{ display: 'none' }}
              onChange={handleFileChange}
            />
          </div>
        }
      >
        {error && <div className={styles.errorBox}>{error}</div>}

        {/* 筛选区域 */}
        <div className={styles.filterSection}>
          {renderFilterTags()}

          <div className={styles.filterControls}>
            <div className={styles.filterItem}>
              <label>{t('auth_files.search_label')}</label>
              <Input
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                  setPage(1);
                }}
                placeholder={t('auth_files.search_placeholder')}
              />
            </div>
            <div className={styles.filterItem}>
              <label>{t('auth_files.page_size_label')}</label>
              <select
                className={styles.pageSizeSelect}
                value={pageSize}
                onChange={(e) => {
                  setPageSize(Number(e.target.value) || 9);
                  setPage(1);
                }}
              >
                <option value={6}>6</option>
                <option value={9}>9</option>
                <option value={12}>12</option>
                <option value={18}>18</option>
                <option value={24}>24</option>
              </select>
            </div>
            <div className={styles.filterItem}>
              <label>{t('common.info')}</label>
              <div className={styles.statsInfo}>
                {files.length} {t('auth_files.files_count')} · {formatFileSize(totalSize)}
              </div>
            </div>
            <div className={styles.filterItem}>
              <label>配额刷新间隔（分钟）</label>
              <Input
                type="number"
                value={quotaRefreshInterval}
                onChange={(e) => {
                  const value = parseInt(e.target.value) || 1;
                  setQuotaRefreshInterval(Math.max(1, Math.min(60, value))); // 限制在1-60分钟之间
                }}
                placeholder="1"
                min={1}
                max={60}
                style={{ width: '100px' }}
              />
            </div>
          </div>
        </div>

        {/* 卡片网格 */}
        {loading ? (
          <div className={styles.hint}>{t('common.loading')}</div>
        ) : pageItems.length === 0 ? (
          <EmptyState title={t('auth_files.search_empty_title')} description={t('auth_files.search_empty_desc')} />
        ) : (
          <div className={styles.fileGrid}>
            {pageItems.map(renderFileCard)}
          </div>
        )}

        {/* 分页 */}
        {!loading && filtered.length > pageSize && (
          <div className={styles.pagination}>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setPage(Math.max(1, currentPage - 1))}
              disabled={currentPage <= 1}
            >
              {t('auth_files.pagination_prev')}
            </Button>
            <div className={styles.pageInfo}>
              {t('auth_files.pagination_info', {
                current: currentPage,
                total: totalPages,
                count: filtered.length
              })}
            </div>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setPage(Math.min(totalPages, currentPage + 1))}
              disabled={currentPage >= totalPages}
            >
              {t('auth_files.pagination_next')}
            </Button>
          </div>
        )}
      </Card>

      {/* OAuth 排除列表卡片 */}
      <Card
        title={t('oauth_excluded.title')}
        extra={
          <Button
            size="sm"
            onClick={() => openExcludedModal()}
            disabled={disableControls || excludedError === 'unsupported'}
          >
            {t('oauth_excluded.add')}
          </Button>
        }
      >
        {excludedError === 'unsupported' ? (
          <EmptyState
            title={t('oauth_excluded.upgrade_required_title')}
            description={t('oauth_excluded.upgrade_required_desc')}
          />
        ) : Object.keys(excluded).length === 0 ? (
          <EmptyState title={t('oauth_excluded.list_empty_all')} />
        ) : (
          <div className={styles.excludedList}>
            {Object.entries(excluded).map(([provider, models]) => (
              <div key={provider} className={styles.excludedItem}>
                <div className={styles.excludedInfo}>
                  <div className={styles.excludedProvider}>{provider}</div>
                  <div className={styles.excludedModels}>
                    {models?.length
                      ? t('oauth_excluded.model_count', { count: models.length })
                      : t('oauth_excluded.no_models')}
                  </div>
                </div>
                <div className={styles.excludedActions}>
                  <Button variant="secondary" size="sm" onClick={() => openExcludedModal(provider)}>
                    {t('common.edit')}
                  </Button>
                  <Button variant="danger" size="sm" onClick={() => deleteExcluded(provider)}>
                    {t('oauth_excluded.delete')}
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      {/* 详情弹窗 */}
      <Modal
        open={detailModalOpen}
        onClose={() => setDetailModalOpen(false)}
        title={selectedFile?.name || t('auth_files.title_section')}
        footer={
          <>
            <Button variant="secondary" onClick={() => setDetailModalOpen(false)}>
              {t('common.close')}
            </Button>
            <Button
              onClick={() => {
                if (selectedFile) {
                  const text = JSON.stringify(selectedFile, null, 2);
                  navigator.clipboard.writeText(text).then(() => {
                    showNotification(t('notification.link_copied'), 'success');
                  });
                }
              }}
            >
              {t('common.copy')}
            </Button>
          </>
        }
      >
        {selectedFile && (
          <div className={styles.detailContent}>
            <pre className={styles.jsonContent}>{JSON.stringify(selectedFile, null, 2)}</pre>
          </div>
        )}
      </Modal>

      {/* 模型列表弹窗 */}
      <Modal
        open={modelsModalOpen}
        onClose={() => setModelsModalOpen(false)}
        title={t('auth_files.models_title', { defaultValue: '支持的模型' }) + ` - ${modelsFileName}`}
        footer={
          <Button variant="secondary" onClick={() => setModelsModalOpen(false)}>
            {t('common.close')}
          </Button>
        }
      >
        {modelsLoading ? (
          <div className={styles.hint}>{t('auth_files.models_loading', { defaultValue: '正在加载模型列表...' })}</div>
        ) : modelsError === 'unsupported' ? (
          <EmptyState
            title={t('auth_files.models_unsupported', { defaultValue: '当前版本不支持此功能' })}
            description={t('auth_files.models_unsupported_desc', { defaultValue: '请更新 CLI Proxy API 到最新版本后重试' })}
          />
        ) : modelsList.length === 0 ? (
          <EmptyState
            title={t('auth_files.models_empty', { defaultValue: '该凭证暂无可用模型' })}
            description={t('auth_files.models_empty_desc', { defaultValue: '该认证凭证可能尚未被服务器加载或没有绑定任何模型' })}
          />
        ) : (
          <div className={styles.modelsList}>
            {modelsList.map((model) => {
              const isExcluded = isModelExcluded(model.id, modelsFileType);
              return (
                <div
                  key={model.id}
                  className={`${styles.modelItem} ${isExcluded ? styles.modelItemExcluded : ''}`}
                  onClick={() => {
                    navigator.clipboard.writeText(model.id);
                    showNotification(t('notification.link_copied', { defaultValue: '已复制到剪贴板' }), 'success');
                  }}
                  title={isExcluded ? t('auth_files.models_excluded_hint', { defaultValue: '此模型已被 OAuth 排除' }) : t('common.copy', { defaultValue: '点击复制' })}
                >
                  <span className={styles.modelId}>{model.id}</span>
                  {model.display_name && model.display_name !== model.id && (
                    <span className={styles.modelDisplayName}>{model.display_name}</span>
                  )}
                  {model.type && (
                    <span className={styles.modelType}>{model.type}</span>
                  )}
                  {isExcluded && (
                    <span className={styles.modelExcludedBadge}>{t('auth_files.models_excluded_badge', { defaultValue: '已排除' })}</span>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </Modal>

      {/* OAuth 排除弹窗 */}
      <Modal
        open={excludedModalOpen}
        onClose={() => setExcludedModalOpen(false)}
        title={t('oauth_excluded.add_title')}
        footer={
          <>
            <Button variant="secondary" onClick={() => setExcludedModalOpen(false)} disabled={savingExcluded}>
              {t('common.cancel')}
            </Button>
            <Button onClick={saveExcludedModels} loading={savingExcluded}>
              {t('oauth_excluded.save')}
            </Button>
          </>
        }
      >
        <Input
          label={t('oauth_excluded.provider_label')}
          placeholder={t('oauth_excluded.provider_placeholder')}
          value={excludedForm.provider}
          onChange={(e) => setExcludedForm((prev) => ({ ...prev, provider: e.target.value }))}
        />
        <div className={styles.formGroup}>
          <label>{t('oauth_excluded.models_label')}</label>
          <textarea
            className={styles.textarea}
            rows={4}
            placeholder={t('oauth_excluded.models_placeholder')}
            value={excludedForm.modelsText}
            onChange={(e) => setExcludedForm((prev) => ({ ...prev, modelsText: e.target.value }))}
          />
          <div className={styles.hint}>{t('oauth_excluded.models_hint')}</div>
        </div>
      </Modal>
    </div>
  );
}
