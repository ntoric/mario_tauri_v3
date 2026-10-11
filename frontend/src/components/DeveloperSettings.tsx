import React, { useState, useEffect } from 'react';
import { Save, Github, Loader2, AlertCircle, Check, ExternalLink, Sparkles, RefreshCw, Eye, EyeOff, Smartphone, Copy, Trash2 } from 'lucide-react';
import { useAuthStore } from '../stores';
import { usePageHeader } from '../contexts/PageHeaderContext';
import { api } from '../services/api';
import { invoke } from '@tauri-apps/api/core';
import ConfirmDialog from './ConfirmDialog';

interface GeminiModel {
  name: string;
  displayName?: string;
  supportedGenerationMethods?: string[];
}

const DeveloperSettings: React.FC = () => {
  const { user } = useAuthStore();
  const { setHeaderContent } = usePageHeader();

  // Update repo config
  const [githubRepo, setGithubRepo] = useState('');
  const [isSaving, setIsSaving] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [saveMessage, setSaveMessage] = useState('');
  const [error, setError] = useState('');

  // LAN server info (for mobile apps on the same network)
  const [lanInfo, setLanInfo] = useState<{ url: string; ip: string | null; port: number } | null>(null);
  const [copied, setCopied] = useState(false);

  // AI provider selection
  const [aiProvider, setAiProvider] = useState<'gemini' | 'openrouter'>('gemini');
  const [isSavingProvider, setIsSavingProvider] = useState(false);
  const [providerMessage, setProviderMessage] = useState('');
  const [providerError, setProviderError] = useState('');

  // Gemini config
  const [geminiApiKey, setGeminiApiKey] = useState('');
  const [geminiModel, setGeminiModel] = useState('');
  const [showApiKey, setShowApiKey] = useState(false);
  const [models, setModels] = useState<GeminiModel[]>([]);
  const [isSavingGemini, setIsSavingGemini] = useState(false);
  const [isLoadingModels, setIsLoadingModels] = useState(false);
  const [geminiMessage, setGeminiMessage] = useState('');
  const [geminiError, setGeminiError] = useState('');

  // OpenRouter config
  const [openrouterApiKey, setOpenrouterApiKey] = useState('');
  const [openrouterModel, setOpenrouterModel] = useState('');
  const [showOpenRouterApiKey, setShowOpenRouterApiKey] = useState(false);
  const [openrouterModels, setOpenrouterModels] = useState<GeminiModel[]>([]);
  const [isSavingOpenRouter, setIsSavingOpenRouter] = useState(false);
  const [isLoadingOpenRouterModels, setIsLoadingOpenRouterModels] = useState(false);
  const [openrouterMessage, setOpenrouterMessage] = useState('');
  const [openrouterError, setOpenrouterError] = useState('');

  // Clear local database
  const [showClearConfirm, setShowClearConfirm] = useState(false);
  const [isClearing, setIsClearing] = useState(false);
  const [clearError, setClearError] = useState('');

  useEffect(() => {
    setHeaderContent({
      title: 'Developer Settings',
      subtitle: 'Configure update repository and AI menu parsing - Superadmin only',
      actions: null,
    });
  }, [setHeaderContent]);

  useEffect(() => {
    if (user?.role !== 'superadmin') {
      setError('Access denied. Superadmin role required.');
      setIsLoading(false);
      return;
    }
    fetchConfig();
    fetchAIProvider();
    fetchGeminiConfig();
    fetchOpenRouterConfig();
    invoke<{ url: string; ip: string | null; port: number }>('lan_server_info')
      .then(setLanInfo)
      .catch(() => setLanInfo(null));
  }, [user]);

  const fetchConfig = async () => {
    try {
      const data = await api.getUpdateRepoConfig();
      setGithubRepo(data.githubRepo || '');
    } catch (err: any) {
      setError(err.message || 'Failed to load update repository configuration');
    } finally {
      setIsLoading(false);
    }
  };

  const fetchAIProvider = async () => {
    try {
      const data = await api.getAIProvider();
      setAiProvider(data.provider === 'openrouter' ? 'openrouter' : 'gemini');
    } catch (err: any) {
      setProviderError(err.message || 'Failed to load AI provider');
    }
  };

  const fetchGeminiConfig = async () => {
    try {
      const data = await api.getGeminiConfig();
      setGeminiApiKey(data.apiKey || '');
      setGeminiModel(data.model || '');
      if (data.model) {
        // Seed the dropdown with the currently configured model so it's visible
        // even before the admin clicks refresh.
        setModels([{ name: data.model, displayName: data.model }]);
      }
    } catch (err: any) {
      setGeminiError(err.message || 'Failed to load Gemini configuration');
    }
  };

  const fetchOpenRouterConfig = async () => {
    try {
      const data = await api.getOpenRouterConfig();
      setOpenrouterApiKey(data.apiKey || '');
      setOpenrouterModel(data.model || '');
      if (data.model) {
        // Seed the dropdown with the currently configured model so it's visible
        // even before the admin clicks refresh.
        setOpenrouterModels([{ name: data.model, displayName: data.model }]);
      }
    } catch (err: any) {
      setOpenrouterError(err.message || 'Failed to load OpenRouter configuration');
    }
  };

  const handleProviderChange = async (e: React.ChangeEvent<HTMLSelectElement>) => {
    const provider = (e.target.value === 'openrouter' ? 'openrouter' : 'gemini') as 'gemini' | 'openrouter';
    setAiProvider(provider);
    setProviderError('');
    setProviderMessage('');
    setIsSavingProvider(true);
    try {
      const res = await api.updateAIProvider(provider);
      setProviderMessage(res.message || 'AI provider saved successfully!');
      setTimeout(() => setProviderMessage(''), 3000);
    } catch (err: any) {
      setProviderError(err.message || 'Failed to save AI provider');
    } finally {
      setIsSavingProvider(false);
    }
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    const repo = githubRepo.trim();
    if (!repo) {
      setError('GitHub repository is required');
      return;
    }
    if (repo.split('/').length !== 2 || repo.startsWith('/') || repo.endsWith('/')) {
      setError("Repository must be in 'owner/repo' format (e.g. ntoric/mario_tauri)");
      return;
    }

    setIsSaving(true);
    setSaveMessage('');
    setError('');

    try {
      const res = await api.updateUpdateRepoConfig(repo);
      setGithubRepo(res.config?.githubRepo || repo);
      setSaveMessage(res.message || 'Configuration saved successfully!');
      setTimeout(() => setSaveMessage(''), 3000);
    } catch (err: any) {
      setError(err.message || 'Failed to save configuration');
    } finally {
      setIsSaving(false);
    }
  };

  const handleRefreshModels = async () => {
    setGeminiError('');
    setGeminiMessage('');
    // Persist the current API key first so the backend can use it to list models.
    const key = geminiApiKey.trim();
    if (!key) {
      setGeminiError('Enter and save a Gemini API key before refreshing models.');
      return;
    }
    setIsLoadingModels(true);
    try {
      // Save the key so the backend has it available for the models request.
      await api.updateGeminiConfig({ apiKey: key, model: geminiModel });
      const data = await api.listGeminiModels();
      const list: GeminiModel[] = data.models || [];
      setModels(list);
      if (list.length === 0) {
        setGeminiError('No models supporting generateContent were returned for this API key.');
      } else if (!geminiModel || !list.find(m => m.name === geminiModel || m.name === `models/${geminiModel}`)) {
        // Default to the first returned model if the current one isn't available.
        const first = list[0];
        setGeminiModel(first.name);
      }
      setGeminiMessage(`Loaded ${list.length} available model(s).`);
      setTimeout(() => setGeminiMessage(''), 3000);
    } catch (err: any) {
      setGeminiError(err.message || 'Failed to load Gemini models');
    } finally {
      setIsLoadingModels(false);
    }
  };

  const handleSaveGemini = async (e: React.FormEvent) => {
    e.preventDefault();
    setGeminiError('');
    setGeminiMessage('');
    const key = geminiApiKey.trim();
    if (!key) {
      setGeminiError('Gemini API key is required');
      return;
    }
    const model = geminiModel.trim();
    if (!model) {
      setGeminiError('Please select a model');
      return;
    }
    setIsSavingGemini(true);
    try {
      const res = await api.updateGeminiConfig({ apiKey: key, model });
      setGeminiModel(res.config?.model || model);
      setGeminiMessage(res.message || 'Gemini configuration saved successfully!');
      setTimeout(() => setGeminiMessage(''), 3000);
    } catch (err: any) {
      setGeminiError(err.message || 'Failed to save Gemini configuration');
    } finally {
      setIsSavingGemini(false);
    }
  };

  const handleRefreshOpenRouterModels = async () => {
    setOpenrouterError('');
    setOpenrouterMessage('');
    // Persist the current API key first so the backend can use it to list models.
    const key = openrouterApiKey.trim();
    if (!key) {
      setOpenrouterError('Enter and save an OpenRouter API key before refreshing models.');
      return;
    }
    setIsLoadingOpenRouterModels(true);
    try {
      // Save the key so the backend has it available for the models request.
      await api.updateOpenRouterConfig({ apiKey: key, model: openrouterModel });
      const data = await api.listOpenRouterModels();
      const list: GeminiModel[] = data.models || [];
      setOpenrouterModels(list);
      if (list.length === 0) {
        setOpenrouterError('No vision-capable models were returned for this API key.');
      } else if (!openrouterModel || !list.find(m => m.name === openrouterModel)) {
        // Default to the first returned model if the current one isn't available.
        const first = list[0];
        setOpenrouterModel(first.name);
      }
      setOpenrouterMessage(`Loaded ${list.length} available model(s).`);
      setTimeout(() => setOpenrouterMessage(''), 3000);
    } catch (err: any) {
      setOpenrouterError(err.message || 'Failed to load OpenRouter models');
    } finally {
      setIsLoadingOpenRouterModels(false);
    }
  };

  const handleSaveOpenRouter = async (e: React.FormEvent) => {
    e.preventDefault();
    setOpenrouterError('');
    setOpenrouterMessage('');
    const key = openrouterApiKey.trim();
    if (!key) {
      setOpenrouterError('OpenRouter API key is required');
      return;
    }
    const model = openrouterModel.trim();
    if (!model) {
      setOpenrouterError('Please select a model');
      return;
    }
    setIsSavingOpenRouter(true);
    try {
      const res = await api.updateOpenRouterConfig({ apiKey: key, model });
      setOpenrouterModel(res.config?.model || model);
      setOpenrouterMessage(res.message || 'OpenRouter configuration saved successfully!');
      setTimeout(() => setOpenrouterMessage(''), 3000);
    } catch (err: any) {
      setOpenrouterError(err.message || 'Failed to save OpenRouter configuration');
    } finally {
      setIsSavingOpenRouter(false);
    }
  };

  const handleClearLocalDb = async () => {
    setShowClearConfirm(false);
    setIsClearing(true);
    setClearError('');
    try {
      await api.clearLocalDb();
    } catch (err: any) {
      setClearError(err.message || 'Failed to clear local database');
      setIsClearing(false);
      return;
    }
    // Wipe client-side state (tokens, persisted auth, caches) and restart at
    // the login screen — the in-memory stores reset on reload.
    localStorage.clear();
    window.location.hash = '#/login';
    window.location.reload();
  };

  if (isLoading) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', padding: '4rem' }}>
        <Loader2 size={32} className="animate-spin" />
      </div>
    );
  }

  if (user?.role !== 'superadmin') {
    return (
      <div style={{ textAlign: 'center', padding: '4rem' }}>
        <AlertCircle size={64} style={{ color: 'var(--danger)', marginBottom: '1.5rem' }} />
        <p style={{ fontSize: '1.125rem', color: 'var(--danger)' }}>Access Denied</p>
        <p style={{ color: 'var(--gray-500)', marginTop: '0.5rem' }}>Only superadmin can access developer settings.</p>
      </div>
    );
  }

  const renderBanner = (msg: string, err: string) => (
    <>
      {msg && (
        <div style={{
          padding: '1rem',
          background: 'rgba(43,165,74, 0.1)',
          color: 'var(--success)',
          borderRadius: 'var(--radius)',
          marginBottom: '1.5rem',
          display: 'flex',
          alignItems: 'center',
          gap: '0.5rem',
        }}>
          <Check size={18} />
          {msg}
        </div>
      )}
      {err && (
        <div style={{
          padding: '1rem',
          background: 'rgba(229,57,53, 0.1)',
          color: 'var(--danger)',
          borderRadius: 'var(--radius)',
          marginBottom: '1.5rem',
          display: 'flex',
          alignItems: 'center',
          gap: '0.5rem',
        }}>
          <AlertCircle size={18} />
          {err}
        </div>
      )}
    </>
  );

  const copyLanUrl = async () => {
    if (!lanInfo?.url) return;
    try {
      await navigator.clipboard.writeText(lanInfo.url);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard unavailable — user can select the text manually.
    }
  };

  return (
    <div>
      {renderBanner(saveMessage, error)}

      <div className="card" style={{ marginBottom: '1.5rem' }}>
        <div className="card-header">
          <span className="card-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <Smartphone size={18} />
            Mobile App Connection (LAN)
          </span>
        </div>
        <div className="card-body">
          <div style={{
            padding: '1rem',
            background: 'var(--gray-50)',
            borderRadius: 'var(--radius)',
            marginBottom: '1.5rem',
            border: '1px solid var(--gray-200)',
          }}>
            <p style={{ fontSize: '0.875rem', color: 'var(--gray-600)', margin: 0 }}>
              <strong>Note:</strong> Mobile apps connect directly to this computer over the local
              network — no internet required. On the mobile app's login screen, tap the server
              row and choose <strong>"Find on this network"</strong>, or enter this address manually.
            </p>
          </div>

          <div className="form-group">
            <label>Server Address</label>
            <div style={{ display: 'flex', gap: '0.5rem' }}>
              <input
                type="text"
                value={lanInfo?.url ?? ''}
                readOnly
                placeholder="LAN server starting…"
                spellCheck={false}
                style={{ flex: 1, fontFamily: 'monospace' }}
              />
              <button
                type="button"
                className="btn btn-outline"
                onClick={copyLanUrl}
                disabled={!lanInfo?.url}
                title="Copy address"
                style={{ flexShrink: 0, display: 'flex', alignItems: 'center', gap: '0.35rem' }}
              >
                {copied ? <Check size={16} /> : <Copy size={16} />}
                {copied ? 'Copied' : 'Copy'}
              </button>
            </div>
            <small style={{ color: 'var(--gray-500)', display: 'block', marginTop: '0.25rem' }}>
              {lanInfo?.ip
                ? `This computer is reachable at ${lanInfo.ip}:${lanInfo.port} on the local network.`
                : 'Could not determine the local IP address.'}
            </small>
          </div>
        </div>
      </div>

      <div className="card" style={{ marginBottom: '1.5rem' }}>
        <div className="card-header">
          <span className="card-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <Github size={18} />
            Update Repository Configuration
          </span>
        </div>
        <form onSubmit={handleSave}>
          <div className="card-body">
            <div style={{
              padding: '1rem',
              background: 'var(--gray-50)',
              borderRadius: 'var(--radius)',
              marginBottom: '1.5rem',
              border: '1px solid var(--gray-200)',
            }}>
              <p style={{ fontSize: '0.875rem', color: 'var(--gray-600)', margin: 0 }}>
                <strong>Note:</strong> This is the GitHub repository the desktop app will check for updates.
                The app fetches the <code>latest.json</code> manifest from this repository's latest release.
                Ensure releases are published with a signed <code>latest.json</code> and installer artifacts.
              </p>
            </div>

            <div className="form-group">
              <label>
                <Github size={16} style={{ marginRight: '0.5rem', verticalAlign: 'middle' }} />
                GitHub Repository
              </label>
              <input
                type="text"
                value={githubRepo}
                onChange={(e) => setGithubRepo(e.target.value)}
                placeholder="owner/repo (e.g. ntoric/mario_tauri)"
                spellCheck={false}
              />
              <small style={{ color: 'var(--gray-500)', display: 'block', marginTop: '0.25rem' }}>
                Repository in <code>owner/repo</code> format. The latest release must include a <code>latest.json</code> manifest.
              </small>
            </div>

            {githubRepo && (
              <div style={{ marginTop: '0.5rem' }}>
                <a
                  href={`https://github.com/${githubRepo.trim()}/releases/latest`}
                  target="_blank"
                  rel="noopener noreferrer"
                  style={{ fontSize: '0.85rem', color: 'var(--primary)', display: 'inline-flex', alignItems: 'center', gap: '0.25rem' }}
                >
                  View latest release <ExternalLink size={12} />
                </a>
              </div>
            )}
          </div>
          <div className="card-footer" style={{ display: 'flex', justifyContent: 'flex-end', padding: '1rem 1.5rem', borderTop: '1px solid var(--gray-200)' }}>
            <button type="submit" className="btn btn-primary" disabled={isSaving}>
              {isSaving ? (
                <>
                  <Loader2 size={18} className="animate-spin" style={{ marginRight: '0.5rem' }} />
                  Saving...
                </>
              ) : (
                <>
                  <Save size={18} style={{ marginRight: '0.5rem' }} />
                  Save Configuration
                </>
              )}
            </button>
          </div>
        </form>
      </div>

      {renderBanner(providerMessage, providerError)}

      <div className="card" style={{ marginBottom: '1.5rem' }}>
        <div className="card-header">
          <span className="card-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <Sparkles size={18} />
            AI Menu Upload Provider
          </span>
        </div>
        <div className="card-body">
          <div style={{
            padding: '1rem',
            background: 'var(--gray-50)',
            borderRadius: 'var(--radius)',
            marginBottom: '1.5rem',
            border: '1px solid var(--gray-200)',
          }}>
            <p style={{ fontSize: '0.875rem', color: 'var(--gray-600)', margin: 0 }}>
              <strong>Note:</strong> The AI Menu Upload feature uses the selected provider to parse
              menu images/PDFs into categories and items. Configure the API key and model for each
              provider in the sections below.
            </p>
          </div>

          <div className="form-group" style={{ marginBottom: 0 }}>
            <label>Active Provider</label>
            <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
              <select
                value={aiProvider}
                onChange={handleProviderChange}
                disabled={isSavingProvider}
                style={{ flex: 1 }}
              >
                <option value="gemini">Google Gemini</option>
                <option value="openrouter">OpenRouter</option>
              </select>
              {isSavingProvider && <Loader2 size={18} className="animate-spin" />}
            </div>
            <small style={{ color: 'var(--gray-500)', display: 'block', marginTop: '0.25rem' }}>
              Saved automatically. The AI Menu Upload page can still override the provider per parse.
            </small>
          </div>
        </div>
      </div>

      {renderBanner(geminiMessage, geminiError)}

      <div className="card">
        <div className="card-header">
          <span className="card-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <Sparkles size={18} />
            Gemini AI Configuration
            {aiProvider === 'gemini' && (
              <span style={{ fontSize: '0.7rem', fontWeight: 600, color: 'var(--success)', background: 'rgba(43,165,74,0.12)', borderRadius: '999px', padding: '0.1rem 0.6rem' }}>
                ACTIVE
              </span>
            )}
          </span>
        </div>
        <form onSubmit={handleSaveGemini}>
          <div className="card-body">
            <div style={{
              padding: '1rem',
              background: 'var(--gray-50)',
              borderRadius: 'var(--radius)',
              marginBottom: '1.5rem',
              border: '1px solid var(--gray-200)',
            }}>
              <p style={{ fontSize: '0.875rem', color: 'var(--gray-600)', margin: 0 }}>
                <strong>Note:</strong> The Gemini API key is used by the AI Menu Upload feature to parse
                menu images/PDFs into categories and items. The key is stored server-side and never
                exposed to clients. Get a key from{' '}
                <a href="https://aistudio.google.com/app/apikey" target="_blank" rel="noopener noreferrer" style={{ color: 'var(--primary)' }}>
                  Google AI Studio <ExternalLink size={11} style={{ verticalAlign: 'middle' }} />
                </a>.
              </p>
            </div>

            <div className="form-group">
              <label>API Key</label>
              <div style={{ display: 'flex', gap: '0.5rem' }}>
                <input
                  type={showApiKey ? 'text' : 'password'}
                  value={geminiApiKey}
                  onChange={(e) => setGeminiApiKey(e.target.value)}
                  placeholder="AIza..."
                  spellCheck={false}
                  autoComplete="off"
                  style={{ flex: 1 }}
                />
                <button
                  type="button"
                  className="btn btn-outline"
                  onClick={() => setShowApiKey(!showApiKey)}
                  title={showApiKey ? 'Hide' : 'Show'}
                  style={{ flexShrink: 0 }}
                >
                  {showApiKey ? <EyeOff size={18} /> : <Eye size={18} />}
                </button>
              </div>
              <small style={{ color: 'var(--gray-500)', display: 'block', marginTop: '0.25rem' }}>
                Stored securely in server settings. Used to authenticate Gemini API requests.
              </small>
            </div>

            <div className="form-group">
              <label>Model</label>
              <div style={{ display: 'flex', gap: '0.5rem' }}>
                <select
                  value={geminiModel}
                  onChange={(e) => setGeminiModel(e.target.value)}
                  style={{ flex: 1 }}
                >
                  {models.length === 0 && (
                    <option value="">Click refresh to load available models</option>
                  )}
                  {models.map((m) => (
                    <option key={m.name} value={m.name}>
                      {m.displayName || m.name}
                    </option>
                  ))}
                </select>
                <button
                  type="button"
                  className="btn btn-outline"
                  onClick={handleRefreshModels}
                  disabled={isLoadingModels}
                  title="Refresh available models from Gemini"
                  style={{ flexShrink: 0, display: 'flex', alignItems: 'center', gap: '0.5rem' }}
                >
                  {isLoadingModels ? (
                    <Loader2 size={18} className="animate-spin" />
                  ) : (
                    <RefreshCw size={18} />
                  )}
                  Refresh
                </button>
              </div>
              <small style={{ color: 'var(--gray-500)', display: 'block', marginTop: '0.25rem' }}>
                Click <strong>Refresh</strong> to fetch the list of models available for your API key.
                Only models supporting <code>generateContent</code> are shown.
              </small>
            </div>
          </div>
          <div className="card-footer" style={{ display: 'flex', justifyContent: 'flex-end', padding: '1rem 1.5rem', borderTop: '1px solid var(--gray-200)' }}>
            <button type="submit" className="btn btn-primary" disabled={isSavingGemini}>
              {isSavingGemini ? (
                <>
                  <Loader2 size={18} className="animate-spin" style={{ marginRight: '0.5rem' }} />
                  Saving...
                </>
              ) : (
                <>
                  <Save size={18} style={{ marginRight: '0.5rem' }} />
                  Save Gemini Configuration
                </>
              )}
            </button>
          </div>
        </form>
      </div>

      {renderBanner(openrouterMessage, openrouterError)}

      <div className="card" style={{ marginTop: '1.5rem' }}>
        <div className="card-header">
          <span className="card-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <Sparkles size={18} />
            OpenRouter AI Configuration
            {aiProvider === 'openrouter' && (
              <span style={{ fontSize: '0.7rem', fontWeight: 600, color: 'var(--success)', background: 'rgba(43,165,74,0.12)', borderRadius: '999px', padding: '0.1rem 0.6rem' }}>
                ACTIVE
              </span>
            )}
          </span>
        </div>
        <form onSubmit={handleSaveOpenRouter}>
          <div className="card-body">
            <div style={{
              padding: '1rem',
              background: 'var(--gray-50)',
              borderRadius: 'var(--radius)',
              marginBottom: '1.5rem',
              border: '1px solid var(--gray-200)',
            }}>
              <p style={{ fontSize: '0.875rem', color: 'var(--gray-600)', margin: 0 }}>
                <strong>Note:</strong> The OpenRouter API key is used by the AI Menu Upload feature to parse
                menu images/PDFs through OpenRouter's OpenAI-compatible API. The key is stored server-side
                and never exposed to clients. Get a key from{' '}
                <a href="https://openrouter.ai/keys" target="_blank" rel="noopener noreferrer" style={{ color: 'var(--primary)' }}>
                  OpenRouter <ExternalLink size={11} style={{ verticalAlign: 'middle' }} />
                </a>.
              </p>
            </div>

            <div className="form-group">
              <label>API Key</label>
              <div style={{ display: 'flex', gap: '0.5rem' }}>
                <input
                  type={showOpenRouterApiKey ? 'text' : 'password'}
                  value={openrouterApiKey}
                  onChange={(e) => setOpenrouterApiKey(e.target.value)}
                  placeholder="sk-or-..."
                  spellCheck={false}
                  autoComplete="off"
                  style={{ flex: 1 }}
                />
                <button
                  type="button"
                  className="btn btn-outline"
                  onClick={() => setShowOpenRouterApiKey(!showOpenRouterApiKey)}
                  title={showOpenRouterApiKey ? 'Hide' : 'Show'}
                  style={{ flexShrink: 0 }}
                >
                  {showOpenRouterApiKey ? <EyeOff size={18} /> : <Eye size={18} />}
                </button>
              </div>
              <small style={{ color: 'var(--gray-500)', display: 'block', marginTop: '0.25rem' }}>
                Stored securely in server settings. Used to authenticate OpenRouter API requests.
              </small>
            </div>

            <div className="form-group">
              <label>Model</label>
              <div style={{ display: 'flex', gap: '0.5rem' }}>
                <select
                  value={openrouterModel}
                  onChange={(e) => setOpenrouterModel(e.target.value)}
                  style={{ flex: 1 }}
                >
                  {openrouterModels.length === 0 && (
                    <option value="">Click refresh to load available models</option>
                  )}
                  {openrouterModels.map((m) => (
                    <option key={m.name} value={m.name}>
                      {m.displayName || m.name}
                    </option>
                  ))}
                </select>
                <button
                  type="button"
                  className="btn btn-outline"
                  onClick={handleRefreshOpenRouterModels}
                  disabled={isLoadingOpenRouterModels}
                  title="Refresh available models from OpenRouter"
                  style={{ flexShrink: 0, display: 'flex', alignItems: 'center', gap: '0.5rem' }}
                >
                  {isLoadingOpenRouterModels ? (
                    <Loader2 size={18} className="animate-spin" />
                  ) : (
                    <RefreshCw size={18} />
                  )}
                  Refresh
                </button>
              </div>
              <small style={{ color: 'var(--gray-500)', display: 'block', marginTop: '0.25rem' }}>
                Click <strong>Refresh</strong> to fetch the list of models available for your API key.
                Only vision-capable models (required for menu parsing) are shown.
              </small>
            </div>
          </div>
          <div className="card-footer" style={{ display: 'flex', justifyContent: 'flex-end', padding: '1rem 1.5rem', borderTop: '1px solid var(--gray-200)' }}>
            <button type="submit" className="btn btn-primary" disabled={isSavingOpenRouter}>
              {isSavingOpenRouter ? (
                <>
                  <Loader2 size={18} className="animate-spin" style={{ marginRight: '0.5rem' }} />
                  Saving...
                </>
              ) : (
                <>
                  <Save size={18} style={{ marginRight: '0.5rem' }} />
                  Save OpenRouter Configuration
                </>
              )}
            </button>
          </div>
        </form>
      </div>

      <div className="card" style={{ marginTop: '1.5rem', border: '1px solid rgba(239, 68, 68, 0.4)' }}>
        <div className="card-header">
          <span className="card-title" style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', color: 'var(--danger)' }}>
            <Trash2 size={18} />
            Danger Zone — Local Data
          </span>
        </div>
        <div className="card-body">
          <div style={{
            padding: '1rem',
            background: 'rgba(239, 68, 68, 0.06)',
            borderRadius: 'var(--radius)',
            marginBottom: '1.5rem',
            border: '1px solid rgba(239, 68, 68, 0.2)',
          }}>
            <p style={{ fontSize: '0.875rem', color: 'var(--gray-600)', margin: 0 }}>
              <strong>Warning:</strong> This permanently deletes <strong>all locally stored data</strong> —
              stores, users, menu, tables, orders, bills, sessions, and any unsynced changes —
              and restores factory defaults. The cloud backend and its database are <strong>not</strong> affected.
              You will be signed out and can log in again to pull fresh data.
            </p>
          </div>
          {clearError && (
            <div style={{
              padding: '1rem',
              background: 'rgba(229,57,53, 0.1)',
              color: 'var(--danger)',
              borderRadius: 'var(--radius)',
              marginBottom: '1rem',
              display: 'flex',
              alignItems: 'center',
              gap: '0.5rem',
            }}>
              <AlertCircle size={18} />
              {clearError}
            </div>
          )}
          <button
            type="button"
            className="btn btn-danger"
            onClick={() => setShowClearConfirm(true)}
            disabled={isClearing}
            style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}
          >
            {isClearing ? (
              <>
                <Loader2 size={18} className="animate-spin" />
                Clearing...
              </>
            ) : (
              <>
                <Trash2 size={18} />
                Clear Local Database
              </>
            )}
          </button>
        </div>
      </div>

      <ConfirmDialog
        isOpen={showClearConfirm}
        title="Clear Local Database?"
        message="All locally stored data will be permanently deleted and you will be signed out. The cloud backend is not affected. This cannot be undone."
        confirmLabel="Clear Local Data"
        variant="danger"
        onConfirm={handleClearLocalDb}
        onCancel={() => setShowClearConfirm(false)}
      />
    </div>
  );
};

export default DeveloperSettings;
