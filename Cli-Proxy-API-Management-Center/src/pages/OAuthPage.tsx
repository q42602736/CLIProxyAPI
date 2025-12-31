import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { useNotificationStore } from '@/stores';
import { oauthApi, type OAuthProvider, type IFlowCookieAuthResponse, type KiroCredentialAuthResponse, type KiroImportResponse } from '@/services/api/oauth';
import styles from './OAuthPage.module.scss';

interface ProviderState {
  url?: string;
  state?: string;
  status?: 'idle' | 'waiting' | 'success' | 'error';
  error?: string;
  polling?: boolean;
  projectId?: string;
  projectIdError?: string;
  callbackUrl?: string;
  callbackSubmitting?: boolean;
  callbackStatus?: 'success' | 'error';
  callbackError?: string;
}

interface IFlowCookieState {
  cookie: string;
  loading: boolean;
  result?: IFlowCookieAuthResponse;
  error?: string;
  errorType?: 'error' | 'warning';
}

interface KiroCredState {
  credPath: string;
  loading: boolean;
  uploading: boolean;
  result?: KiroCredentialAuthResponse;
  error?: string;
  errorType?: 'error' | 'warning';
}

interface KiroImportState {
  jsonText: string;
  loading: boolean;
  result?: KiroImportResponse;
  error?: string;
  errorType?: 'error' | 'warning';
}

const PROVIDERS: { id: OAuthProvider; titleKey: string; hintKey: string; urlLabelKey: string }[] = [
  { id: 'codex', titleKey: 'auth_login.codex_oauth_title', hintKey: 'auth_login.codex_oauth_hint', urlLabelKey: 'auth_login.codex_oauth_url_label' },
  { id: 'anthropic', titleKey: 'auth_login.anthropic_oauth_title', hintKey: 'auth_login.anthropic_oauth_hint', urlLabelKey: 'auth_login.anthropic_oauth_url_label' },
  { id: 'antigravity', titleKey: 'auth_login.antigravity_oauth_title', hintKey: 'auth_login.antigravity_oauth_hint', urlLabelKey: 'auth_login.antigravity_oauth_url_label' },
  { id: 'gemini-cli', titleKey: 'auth_login.gemini_cli_oauth_title', hintKey: 'auth_login.gemini_cli_oauth_hint', urlLabelKey: 'auth_login.gemini_cli_oauth_url_label' },
  { id: 'qwen', titleKey: 'auth_login.qwen_oauth_title', hintKey: 'auth_login.qwen_oauth_hint', urlLabelKey: 'auth_login.qwen_oauth_url_label' },
  { id: 'iflow', titleKey: 'auth_login.iflow_oauth_title', hintKey: 'auth_login.iflow_oauth_hint', urlLabelKey: 'auth_login.iflow_oauth_url_label' }
];

const CALLBACK_SUPPORTED: OAuthProvider[] = ['codex', 'anthropic', 'antigravity', 'gemini-cli', 'iflow'];

export function OAuthPage() {
  const { t } = useTranslation();
  const { showNotification } = useNotificationStore();
  const [states, setStates] = useState<Record<OAuthProvider, ProviderState>>({} as Record<OAuthProvider, ProviderState>);
  const [iflowCookie, setIflowCookie] = useState<IFlowCookieState>({ cookie: '', loading: false });
  const [kiroCred, setKiroCred] = useState<KiroCredState>({ credPath: '', loading: false, uploading: false });
  const [kiroImport, setKiroImport] = useState<KiroImportState>({ jsonText: '', loading: false });
  const timers = useRef<Record<string, number>>({});

  useEffect(() => {
    return () => {
      Object.values(timers.current).forEach((timer) => window.clearInterval(timer));
    };
  }, []);

  const updateProviderState = (provider: OAuthProvider, next: Partial<ProviderState>) => {
    setStates((prev) => ({
      ...prev,
      [provider]: { ...(prev[provider] ?? {}), ...next }
    }));
  };

  const startPolling = (provider: OAuthProvider, state: string) => {
    if (timers.current[provider]) {
      clearInterval(timers.current[provider]);
    }
    const timer = window.setInterval(async () => {
      try {
        const res = await oauthApi.getAuthStatus(state);
        if (res.status === 'ok') {
          updateProviderState(provider, { status: 'success', polling: false });
          showNotification(t('auth_login.codex_oauth_status_success'), 'success');
          window.clearInterval(timer);
          delete timers.current[provider];
        } else if (res.status === 'error') {
          updateProviderState(provider, { status: 'error', error: res.error, polling: false });
          showNotification(`${t('auth_login.codex_oauth_status_error')} ${res.error || ''}`, 'error');
          window.clearInterval(timer);
          delete timers.current[provider];
        }
      } catch (err: any) {
        updateProviderState(provider, { status: 'error', error: err?.message, polling: false });
        window.clearInterval(timer);
        delete timers.current[provider];
      }
    }, 3000);
    timers.current[provider] = timer;
  };

  const startAuth = async (provider: OAuthProvider) => {
    const projectId = provider === 'gemini-cli' ? (states[provider]?.projectId || '').trim() : undefined;
    if (provider === 'gemini-cli' && !projectId) {
      const message = t('auth_login.gemini_cli_project_id_required');
      updateProviderState(provider, { projectIdError: message });
      showNotification(message, 'warning');
      return;
    }
    if (provider === 'gemini-cli') {
      updateProviderState(provider, { projectIdError: undefined });
    }
    updateProviderState(provider, {
      status: 'waiting',
      polling: true,
      error: undefined,
      callbackStatus: undefined,
      callbackError: undefined,
      callbackUrl: ''
    });
    try {
      const res = await oauthApi.startAuth(
        provider,
        provider === 'gemini-cli' ? { projectId: projectId! } : undefined
      );
      updateProviderState(provider, { url: res.url, state: res.state, status: 'waiting', polling: true });
      if (res.state) {
        startPolling(provider, res.state);
      }
    } catch (err: any) {
      updateProviderState(provider, { status: 'error', error: err?.message, polling: false });
      showNotification(`${t('auth_login.codex_oauth_start_error')} ${err?.message || ''}`, 'error');
    }
  };

  const copyLink = async (url?: string) => {
    if (!url) return;
    try {
      await navigator.clipboard.writeText(url);
      showNotification(t('notification.link_copied'), 'success');
    } catch {
      showNotification('Copy failed', 'error');
    }
  };

  const submitCallback = async (provider: OAuthProvider) => {
    const redirectUrl = (states[provider]?.callbackUrl || '').trim();
    if (!redirectUrl) {
      showNotification(t('auth_login.oauth_callback_required'), 'warning');
      return;
    }
    updateProviderState(provider, {
      callbackSubmitting: true,
      callbackStatus: undefined,
      callbackError: undefined
    });
    try {
      await oauthApi.submitCallback(provider, redirectUrl);
      updateProviderState(provider, { callbackSubmitting: false, callbackStatus: 'success' });
      showNotification(t('auth_login.oauth_callback_success'), 'success');
    } catch (err: any) {
      const errorMessage =
        err?.status === 404
          ? t('auth_login.oauth_callback_upgrade_hint', {
              defaultValue: 'Please update CLI Proxy API or check the connection.'
            })
          : err?.message;
      updateProviderState(provider, {
        callbackSubmitting: false,
        callbackStatus: 'error',
        callbackError: errorMessage
      });
      const notificationMessage = errorMessage
        ? `${t('auth_login.oauth_callback_error')} ${errorMessage}`
        : t('auth_login.oauth_callback_error');
      showNotification(notificationMessage, 'error');
    }
  };

  const submitIflowCookie = async () => {
    const cookie = iflowCookie.cookie.trim();
    if (!cookie) {
      showNotification(t('auth_login.iflow_cookie_required'), 'warning');
      return;
    }
    setIflowCookie((prev) => ({
      ...prev,
      loading: true,
      error: undefined,
      errorType: undefined,
      result: undefined
    }));
    try {
      const res = await oauthApi.iflowCookieAuth(cookie);
      if (res.status === 'ok') {
        setIflowCookie((prev) => ({ ...prev, loading: false, result: res }));
        showNotification(t('auth_login.iflow_cookie_status_success'), 'success');
      } else {
        setIflowCookie((prev) => ({
          ...prev,
          loading: false,
          error: res.error,
          errorType: 'error'
        }));
        showNotification(`${t('auth_login.iflow_cookie_status_error')} ${res.error || ''}`, 'error');
      }
    } catch (err: any) {
      if (err?.status === 409) {
        const message = t('auth_login.iflow_cookie_config_duplicate');
        setIflowCookie((prev) => ({ ...prev, loading: false, error: message, errorType: 'warning' }));
        showNotification(message, 'warning');
        return;
      }
      setIflowCookie((prev) => ({ ...prev, loading: false, error: err?.message, errorType: 'error' }));
      showNotification(`${t('auth_login.iflow_cookie_start_error')} ${err?.message || ''}`, 'error');
    }
  };

  const submitKiroCred = async () => {
    const credPath = kiroCred.credPath.trim();
    setKiroCred((prev) => ({
      ...prev,
      loading: true,
      error: undefined,
      errorType: undefined,
      result: undefined
    }));
    try {
      const res = await oauthApi.kiroCredentialAuth(credPath || undefined);
      if (res.status === 'ok') {
        setKiroCred((prev) => ({ ...prev, loading: false, result: res }));
        showNotification(t('auth_login.kiro_status_success'), 'success');
      } else {
        setKiroCred((prev) => ({
          ...prev,
          loading: false,
          error: res.error,
          errorType: 'error'
        }));
        showNotification(`${t('auth_login.kiro_status_error')} ${res.error || ''}`, 'error');
      }
    } catch (err: any) {
      if (err?.status === 409) {
        const message = t('auth_login.kiro_config_duplicate');
        setKiroCred((prev) => ({ ...prev, loading: false, error: message, errorType: 'warning' }));
        showNotification(message, 'warning');
        return;
      }
      setKiroCred((prev) => ({ ...prev, loading: false, error: err?.message, errorType: 'error' }));
      showNotification(`${t('auth_login.kiro_start_error')} ${err?.message || ''}`, 'error');
    }
  };

  const handleKiroFileUpload = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;

    setKiroCred((prev) => ({
      ...prev,
      uploading: true,
      error: undefined,
      errorType: undefined
    }));

    try {
      const res = await oauthApi.kiroUploadCredential(file);
      if (res.status === 'ok' && res.file_path) {
        setKiroCred((prev) => ({ ...prev, uploading: false, credPath: res.file_path || '' }));
        showNotification(t('auth_login.kiro_upload_success'), 'success');
      } else {
        setKiroCred((prev) => ({
          ...prev,
          uploading: false,
          error: res.error,
          errorType: 'error'
        }));
        showNotification(`${t('auth_login.kiro_upload_error')} ${res.error || ''}`, 'error');
      }
    } catch (err: any) {
      setKiroCred((prev) => ({ ...prev, uploading: false, error: err?.message, errorType: 'error' }));
      showNotification(`${t('auth_login.kiro_upload_error')} ${err?.message || ''}`, 'error');
    }

    // Reset file input
    event.target.value = '';
  };

  const submitKiroImport = async () => {
    const jsonText = kiroImport.jsonText.trim();
    if (!jsonText) {
      showNotification(t('auth_login.kiro_import_json_required'), 'warning');
      return;
    }
    let data: Record<string, unknown>;
    try {
      data = JSON.parse(jsonText);
    } catch {
      showNotification(t('auth_login.kiro_import_json_invalid'), 'error');
      return;
    }
    setKiroImport((prev) => ({
      ...prev,
      loading: true,
      error: undefined,
      errorType: undefined,
      result: undefined
    }));
    try {
      const res = await oauthApi.kiroImportAccount(data);
      if (res.status === 'ok') {
        setKiroImport((prev) => ({ ...prev, loading: false, result: res, jsonText: '' }));
        showNotification(t('auth_login.kiro_import_status_success'), 'success');
      } else {
        setKiroImport((prev) => ({
          ...prev,
          loading: false,
          error: res.error,
          errorType: 'error'
        }));
        showNotification(`${t('auth_login.kiro_import_status_error')} ${res.error || ''}`, 'error');
      }
    } catch (err: any) {
      if (err?.status === 409) {
        const message = t('auth_login.kiro_import_config_duplicate');
        setKiroImport((prev) => ({ ...prev, loading: false, error: message, errorType: 'warning' }));
        showNotification(message, 'warning');
        return;
      }
      setKiroImport((prev) => ({ ...prev, loading: false, error: err?.message, errorType: 'error' }));
      showNotification(`${t('auth_login.kiro_import_start_error')} ${err?.message || ''}`, 'error');
    }
  };

  return (
    <div className={styles.container}>
      <h1 className={styles.pageTitle}>{t('nav.oauth', { defaultValue: 'OAuth' })}</h1>

      <div className={styles.content}>
        {PROVIDERS.map((provider) => {
          const state = states[provider.id] || {};
          const canSubmitCallback = CALLBACK_SUPPORTED.includes(provider.id) && Boolean(state.url);
          return (
            <div key={provider.id}>
              <Card
                title={t(provider.titleKey)}
                extra={
                  <Button onClick={() => startAuth(provider.id)} loading={state.polling}>
                    {t('common.login')}
                  </Button>
                }
              >
                <div className="hint">{t(provider.hintKey)}</div>
                {provider.id === 'gemini-cli' && (
                  <Input
                    label={t('auth_login.gemini_cli_project_id_label')}
                    hint={t('auth_login.gemini_cli_project_id_hint')}
                    value={state.projectId || ''}
                    error={state.projectIdError}
                    onChange={(e) =>
                      updateProviderState(provider.id, {
                        projectId: e.target.value,
                        projectIdError: undefined
                      })
                    }
                    placeholder={t('auth_login.gemini_cli_project_id_placeholder')}
                  />
                )}
                {state.url && (
                  <div className={`connection-box ${styles.authUrlBox}`}>
                    <div className={styles.authUrlLabel}>{t(provider.urlLabelKey)}</div>
                    <div className={styles.authUrlValue}>{state.url}</div>
                    <div className={styles.authUrlActions}>
                      <Button variant="secondary" size="sm" onClick={() => copyLink(state.url!)}>
                        {t('auth_login.codex_copy_link')}
                      </Button>
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => window.open(state.url, '_blank', 'noopener,noreferrer')}
                      >
                        {t('auth_login.codex_open_link')}
                      </Button>
                    </div>
                  </div>
                )}
                {canSubmitCallback && (
                  <div className={styles.callbackSection}>
                    <Input
                      label={t('auth_login.oauth_callback_label')}
                      hint={t('auth_login.oauth_callback_hint')}
                      value={state.callbackUrl || ''}
                      onChange={(e) =>
                        updateProviderState(provider.id, {
                          callbackUrl: e.target.value,
                          callbackStatus: undefined,
                          callbackError: undefined
                        })
                      }
                      placeholder={t('auth_login.oauth_callback_placeholder')}
                    />
                    <div className={styles.callbackActions}>
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => submitCallback(provider.id)}
                        loading={state.callbackSubmitting}
                      >
                        {t('auth_login.oauth_callback_button')}
                      </Button>
                    </div>
                    {state.callbackStatus === 'success' && (
                      <div className="status-badge success" style={{ marginTop: 8 }}>
                        {t('auth_login.oauth_callback_status_success')}
                      </div>
                    )}
                    {state.callbackStatus === 'error' && (
                      <div className="status-badge error" style={{ marginTop: 8 }}>
                        {t('auth_login.oauth_callback_status_error')} {state.callbackError || ''}
                      </div>
                    )}
                  </div>
                )}
                {state.status && state.status !== 'idle' && (
                  <div className="status-badge" style={{ marginTop: 8 }}>
                    {state.status === 'success'
                      ? t('auth_login.codex_oauth_status_success')
                      : state.status === 'error'
                        ? `${t('auth_login.codex_oauth_status_error')} ${state.error || ''}`
                        : t('auth_login.codex_oauth_status_waiting')}
                  </div>
                )}
              </Card>
            </div>
          );
        })}

        {/* Kiro 凭证登录 */}
        <Card
          title={t('auth_login.kiro_title')}
          extra={
            <Button onClick={submitKiroCred} loading={kiroCred.loading}>
              {t('auth_login.kiro_button')}
            </Button>
          }
        >
          <div className="hint">{t('auth_login.kiro_hint')}</div>
          <div className="hint" style={{ marginTop: 4 }}>
            {t('auth_login.kiro_path_hint')}
          </div>
          <div className="form-item" style={{ marginTop: 12 }}>
            <label className="label">{t('auth_login.kiro_file_path_label')}</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <div style={{ flex: 1 }}>
                <Input
                  value={kiroCred.credPath}
                  onChange={(e) => setKiroCred((prev) => ({ ...prev, credPath: e.target.value }))}
                  placeholder={t('auth_login.kiro_file_path_placeholder')}
                />
              </div>
              <>
                <input
                  id="kiro-file-upload"
                  type="file"
                  accept=".json"
                  onChange={handleKiroFileUpload}
                  style={{ display: 'none' }}
                />
                <Button
                  variant="secondary"
                  loading={kiroCred.uploading}
                  onClick={() => document.getElementById('kiro-file-upload')?.click()}
                >
                  {t('auth_login.kiro_upload_button')}
                </Button>
              </>
            </div>
            <div className="hint" style={{ marginTop: 4 }}>
              {t('auth_login.kiro_file_path_hint')}
            </div>
          </div>
          {kiroCred.error && (
            <div
              className={`status-badge ${kiroCred.errorType === 'warning' ? 'warning' : 'error'}`}
              style={{ marginTop: 8 }}
            >
              {kiroCred.errorType === 'warning'
                ? t('auth_login.kiro_status_duplicate')
                : t('auth_login.kiro_status_error')}{' '}
              {kiroCred.error}
            </div>
          )}
          {kiroCred.result && kiroCred.result.status === 'ok' && (
            <div className="connection-box" style={{ marginTop: 12 }}>
              <div className="label">{t('auth_login.kiro_result_title')}</div>
              <div className="key-value-list">
                {kiroCred.result.region && (
                  <div className="key-value-item">
                    <span className="key">{t('auth_login.kiro_result_region')}</span>
                    <span className="value">{kiroCred.result.region}</span>
                  </div>
                )}
                {kiroCred.result.saved_path && (
                  <div className="key-value-item">
                    <span className="key">{t('auth_login.kiro_result_path')}</span>
                    <span className="value">{kiroCred.result.saved_path}</span>
                  </div>
                )}
                {kiroCred.result.type && (
                  <div className="key-value-item">
                    <span className="key">{t('auth_login.kiro_result_type')}</span>
                    <span className="value">{kiroCred.result.type}</span>
                  </div>
                )}
              </div>
            </div>
          )}
        </Card>

        {/* Kiro 账号导入 (kiro-account-manager) */}
        <Card
          title={t('auth_login.kiro_import_title')}
          extra={
            <Button onClick={submitKiroImport} loading={kiroImport.loading}>
              {t('auth_login.kiro_import_button')}
            </Button>
          }
        >
          <div className="hint">{t('auth_login.kiro_import_hint')}</div>
          <div className="form-item" style={{ marginTop: 12 }}>
            <label className="label">{t('auth_login.kiro_import_json_label')}</label>
            <textarea
              className="textarea"
              value={kiroImport.jsonText}
              onChange={(e) => setKiroImport((prev) => ({ ...prev, jsonText: e.target.value }))}
              placeholder={t('auth_login.kiro_import_json_placeholder')}
              rows={6}
              style={{ width: '100%', fontFamily: 'monospace', fontSize: 12 }}
            />
          </div>
          {kiroImport.error && (
            <div
              className={`status-badge ${kiroImport.errorType === 'warning' ? 'warning' : 'error'}`}
              style={{ marginTop: 8 }}
            >
              {kiroImport.errorType === 'warning'
                ? t('auth_login.kiro_import_status_duplicate')
                : t('auth_login.kiro_import_status_error')}{' '}
              {kiroImport.error}
            </div>
          )}
          {kiroImport.result && kiroImport.result.status === 'ok' && (
            <div className="connection-box" style={{ marginTop: 12 }}>
              <div className="label">{t('auth_login.kiro_import_result_title')}</div>
              <div className="key-value-list">
                {kiroImport.result.region && (
                  <div className="key-value-item">
                    <span className="key">{t('auth_login.kiro_result_region')}</span>
                    <span className="value">{kiroImport.result.region}</span>
                  </div>
                )}
                {kiroImport.result.auth_method && (
                  <div className="key-value-item">
                    <span className="key">{t('auth_login.kiro_import_result_auth_method')}</span>
                    <span className="value">{kiroImport.result.auth_method}</span>
                  </div>
                )}
                {kiroImport.result.saved_path && (
                  <div className="key-value-item">
                    <span className="key">{t('auth_login.kiro_result_path')}</span>
                    <span className="value">{kiroImport.result.saved_path}</span>
                  </div>
                )}
              </div>
            </div>
          )}
        </Card>

        {/* iFlow Cookie 登录 */}
        <Card
          title={t('auth_login.iflow_cookie_title')}
          extra={
            <Button onClick={submitIflowCookie} loading={iflowCookie.loading}>
              {t('auth_login.iflow_cookie_button')}
            </Button>
          }
        >
          <div className="hint">{t('auth_login.iflow_cookie_hint')}</div>
          <div className="hint" style={{ marginTop: 4 }}>
            {t('auth_login.iflow_cookie_key_hint')}
          </div>
          <div className="form-item" style={{ marginTop: 12 }}>
            <label className="label">{t('auth_login.iflow_cookie_label')}</label>
            <Input
              value={iflowCookie.cookie}
              onChange={(e) => setIflowCookie((prev) => ({ ...prev, cookie: e.target.value }))}
              placeholder={t('auth_login.iflow_cookie_placeholder')}
            />
          </div>
          {iflowCookie.error && (
            <div
              className={`status-badge ${iflowCookie.errorType === 'warning' ? 'warning' : 'error'}`}
              style={{ marginTop: 8 }}
            >
              {iflowCookie.errorType === 'warning'
                ? t('auth_login.iflow_cookie_status_duplicate')
                : t('auth_login.iflow_cookie_status_error')}{' '}
              {iflowCookie.error}
            </div>
          )}
          {iflowCookie.result && iflowCookie.result.status === 'ok' && (
            <div className="connection-box" style={{ marginTop: 12 }}>
              <div className="label">{t('auth_login.iflow_cookie_result_title')}</div>
              <div className="key-value-list">
                {iflowCookie.result.email && (
                  <div className="key-value-item">
                    <span className="key">{t('auth_login.iflow_cookie_result_email')}</span>
                    <span className="value">{iflowCookie.result.email}</span>
                  </div>
                )}
                {iflowCookie.result.expired && (
                  <div className="key-value-item">
                    <span className="key">{t('auth_login.iflow_cookie_result_expired')}</span>
                    <span className="value">{iflowCookie.result.expired}</span>
                  </div>
                )}
                {iflowCookie.result.saved_path && (
                  <div className="key-value-item">
                    <span className="key">{t('auth_login.iflow_cookie_result_path')}</span>
                    <span className="value">{iflowCookie.result.saved_path}</span>
                  </div>
                )}
                {iflowCookie.result.type && (
                  <div className="key-value-item">
                    <span className="key">{t('auth_login.iflow_cookie_result_type')}</span>
                    <span className="value">{iflowCookie.result.type}</span>
                  </div>
                )}
              </div>
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
