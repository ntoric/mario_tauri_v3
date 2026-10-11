package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"cafe-backend/internal/middleware"
	"cafe-backend/internal/models"
)

const geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// defaultGeminiModel is used when no model has been configured yet.
const defaultGeminiModel = "gemini-2.0-flash"

// openRouterBaseURL is the OpenAI-compatible OpenRouter API endpoint.
const openRouterBaseURL = "https://openrouter.ai/api/v1"

// defaultOpenRouterModel is used when no OpenRouter model has been configured yet.
const defaultOpenRouterModel = "google/gemini-2.0-flash-001"

// menuParsePrompt is sent to the AI provider alongside the menu image/PDF. It asks for
// a strict, standardized JSON structure that the backend can parse directly.
const menuParsePrompt = `You are a menu parser. Analyze the provided menu image or PDF and extract every category, item and price exactly as printed.

Return ONLY a JSON object with this exact structure and nothing else (no markdown, no code fences, no commentary):
{
  "categories": [
    {
      "name": "<category name as printed, or 'Uncategorized' if none>",
      "description": "<short category description if present, otherwise empty string>",
      "items": [
        {
          "name": "<item name as printed>",
          "description": "<item description/subtitle if present, otherwise empty string>",
          "price": <numeric price as a number, 0 if not priced>,
          "hsnCode": "<HSN/SAC code if present, otherwise empty string>",
          "taxPercent": <tax percent as a number if present, otherwise 0>
        }
      ]
    }
  ]
}

Rules:
- Always include the "categories" array even if there is only one category.
- If the menu has no explicit categories, put all items under a single category named "Uncategorized".
- Prices must be numbers (e.g. 120, 99.5), never strings. Strip currency symbols.
- Group items under the category they belong to. Do not duplicate items.
- Preserve original spelling and casing of names.
- Do not invent data that is not in the menu.`

// ==========================================
// GEMINI CONFIG HANDLERS
// ==========================================

// GetGeminiConfig handles GET /api/system/gemini-config (superadmin only)
func (h *Handler) GetGeminiConfig(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if claims.Role != "superadmin" {
		h.writeError(w, http.StatusForbidden, "Access denied. Superadmin role required.")
		return
	}

	apiKey, model, err := h.Repo.Gemini.Get(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if model == "" {
		model = defaultGeminiModel
	}

	h.writeJSON(w, http.StatusOK, models.GeminiConfig{
		APIKey: apiKey,
		Model:  model,
	})
}

// UpdateGeminiConfig handles POST /api/system/gemini-config (superadmin only)
func (h *Handler) UpdateGeminiConfig(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if claims.Role != "superadmin" {
		h.writeError(w, http.StatusForbidden, "Access denied. Superadmin role required.")
		return
	}

	var req models.GeminiConfigRequest
	if err := h.readJSON(r, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	apiKey := strings.TrimSpace(req.APIKey)
	model := strings.TrimSpace(req.Model)
	model = strings.TrimPrefix(model, "models/")
	if model == "" {
		model = defaultGeminiModel
	}

	if err := h.Repo.Gemini.Save(r.Context(), apiKey, model); err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Gemini configuration saved successfully",
		"config": models.GeminiConfig{
			APIKey: apiKey,
			Model:  model,
		},
	})
}

// ListGeminiModels handles GET /api/system/gemini-models (superadmin only)
// It calls the Gemini "list models" endpoint using the stored API key so the
// frontend model dropdown can be refreshed with currently available models.
func (h *Handler) ListGeminiModels(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if claims.Role != "superadmin" {
		h.writeError(w, http.StatusForbidden, "Access denied. Superadmin role required.")
		return
	}

	apiKey, _, err := h.Repo.Gemini.Get(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if apiKey == "" {
		h.writeError(w, http.StatusBadRequest, "Gemini API key is not configured. Save an API key first.")
		return
	}

	url := geminiBaseURL + "/models?key=" + apiKey
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to create models request")
		return
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "Failed to contact Gemini API: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		h.writeError(w, http.StatusBadGateway, fmt.Sprintf("Gemini API returned %s: %s", resp.Status, strings.TrimSpace(string(body))))
		return
	}

	var listResp struct {
		Models []models.GeminiModel `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to parse Gemini models response")
		return
	}

	// Keep only models that support generateContent (the method we use for parsing).
	var filtered []models.GeminiModel
	for _, m := range listResp.Models {
		for _, method := range m.SupportedGenerationMethods {
			if method == "generateContent" {
				filtered = append(filtered, m)
				break
			}
		}
	}
	if filtered == nil {
		filtered = []models.GeminiModel{}
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"models": filtered,
	})
}

// ==========================================
// AI PROVIDER HANDLERS
// ==========================================

// GetAIProvider handles GET /api/system/ai-provider (superadmin only)
// It returns the currently active provider plus the configuration status of
// each supported provider so the UI can show which ones are usable.
func (h *Handler) GetAIProvider(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if claims.Role != "superadmin" {
		h.writeError(w, http.StatusForbidden, "Access denied. Superadmin role required.")
		return
	}

	provider, err := h.Repo.AIProvider.Get(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if provider == "" {
		provider = models.AIProviderGemini
	}

	geminiKey, geminiModel, err := h.Repo.Gemini.Get(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	orKey, orModel, err := h.Repo.OpenRouter.Get(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if geminiModel == "" {
		geminiModel = defaultGeminiModel
	}
	if orModel == "" {
		orModel = defaultOpenRouterModel
	}

	status := models.AIProviderStatus{Provider: provider}
	status.Gemini.Configured = geminiKey != ""
	status.Gemini.Model = geminiModel
	status.OpenRouter.Configured = orKey != ""
	status.OpenRouter.Model = orModel

	h.writeJSON(w, http.StatusOK, status)
}

// UpdateAIProvider handles POST /api/system/ai-provider (superadmin only)
func (h *Handler) UpdateAIProvider(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if claims.Role != "superadmin" {
		h.writeError(w, http.StatusForbidden, "Access denied. Superadmin role required.")
		return
	}

	var req models.AIProviderRequest
	if err := h.readJSON(r, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider != models.AIProviderGemini && provider != models.AIProviderOpenRouter {
		h.writeError(w, http.StatusBadRequest, "provider must be 'gemini' or 'openrouter'")
		return
	}

	if err := h.Repo.AIProvider.Save(r.Context(), provider); err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":  "AI provider saved successfully",
		"provider": provider,
	})
}

// ==========================================
// OPENROUTER CONFIG HANDLERS
// ==========================================

// GetOpenRouterConfig handles GET /api/system/openrouter-config (superadmin only)
func (h *Handler) GetOpenRouterConfig(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if claims.Role != "superadmin" {
		h.writeError(w, http.StatusForbidden, "Access denied. Superadmin role required.")
		return
	}

	apiKey, model, err := h.Repo.OpenRouter.Get(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if model == "" {
		model = defaultOpenRouterModel
	}

	h.writeJSON(w, http.StatusOK, models.OpenRouterConfig{
		APIKey: apiKey,
		Model:  model,
	})
}

// UpdateOpenRouterConfig handles POST /api/system/openrouter-config (superadmin only)
func (h *Handler) UpdateOpenRouterConfig(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if claims.Role != "superadmin" {
		h.writeError(w, http.StatusForbidden, "Access denied. Superadmin role required.")
		return
	}

	var req models.OpenRouterConfigRequest
	if err := h.readJSON(r, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	apiKey := strings.TrimSpace(req.APIKey)
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = defaultOpenRouterModel
	}

	if err := h.Repo.OpenRouter.Save(r.Context(), apiKey, model); err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "OpenRouter configuration saved successfully",
		"config": models.OpenRouterConfig{
			APIKey: apiKey,
			Model:  model,
		},
	})
}

// ListOpenRouterModels handles GET /api/system/openrouter-models (superadmin only)
// It calls the OpenRouter "list models" endpoint and returns only models that
// accept image input (required for menu parsing). The returned entries reuse
// the GeminiModel shape (name=OpenRouter model id, displayName=human name) so
// the frontend can render both providers' dropdowns the same way.
func (h *Handler) ListOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if claims.Role != "superadmin" {
		h.writeError(w, http.StatusForbidden, "Access denied. Superadmin role required.")
		return
	}

	apiKey, _, err := h.Repo.OpenRouter.Get(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if apiKey == "" {
		h.writeError(w, http.StatusBadRequest, "OpenRouter API key is not configured. Save an API key first.")
		return
	}

	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, openRouterBaseURL+"/models", nil)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to create models request")
		return
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "Failed to contact OpenRouter API: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		h.writeError(w, http.StatusBadGateway, fmt.Sprintf("OpenRouter API returned %s: %s", resp.Status, strings.TrimSpace(string(body))))
		return
	}

	var listResp struct {
		Data []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Architecture struct {
				InputModalities []string `json:"input_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		h.writeError(w, http.StatusInternalServerError, "Failed to parse OpenRouter models response")
		return
	}

	// Keep only vision-capable models (they must accept image input to parse menus).
	filtered := []models.GeminiModel{}
	for _, m := range listResp.Data {
		vision := false
		for _, mod := range m.Architecture.InputModalities {
			if mod == "image" {
				vision = true
				break
			}
		}
		if !vision {
			continue
		}
		display := m.Name
		if display == "" {
			display = m.ID
		}
		filtered = append(filtered, models.GeminiModel{Name: m.ID, DisplayName: display})
	}
	sort.Slice(filtered, func(i, j int) bool {
		return strings.ToLower(filtered[i].DisplayName) < strings.ToLower(filtered[j].DisplayName)
	})

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"models": filtered,
	})
}

// ==========================================
// MENU PARSE HANDLER
// ==========================================

// ParseMenuImage handles POST /api/menu/parse (superadmin only)
// It forwards the uploaded menu image(s)/PDF(s) (as base64) to the configured
// AI provider (Gemini or OpenRouter) with a strict JSON prompt, parses the
// standardized response and returns both the structured menu and the raw model
// output for debugging. Multiple images may be supplied in a single request
// (e.g. a multi-page menu photographed as several pictures); they are sent as
// separate parts so the model can parse them together as one menu.
//
// The provider is resolved from req.Provider; when empty the stored
// ai_provider setting is used, defaulting to Gemini.
func (h *Handler) ParseMenuImage(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if claims.Role != "superadmin" {
		h.writeError(w, http.StatusForbidden, "Access denied. Superadmin role required.")
		return
	}

	var req models.MenuParseRequest
	if err := h.readJSON(r, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	if strings.TrimSpace(req.StoreID) == "" {
		h.writeError(w, http.StatusBadRequest, "storeId is required")
		return
	}

	// Collect images. The Images array takes precedence; fall back to the
	// legacy single ImageBase64/MimeType fields for backward compatibility.
	var images []inlineDataPart
	for _, img := range req.Images {
		mt, data := normalizeImageData(img.ImageBase64, img.MimeType)
		if data != "" {
			images = append(images, inlineDataPart{MimeType: mt, Data: data})
		}
	}
	if len(images) == 0 && req.ImageBase64 != "" {
		mt, data := normalizeImageData(req.ImageBase64, req.MimeType)
		if data != "" {
			images = append(images, inlineDataPart{MimeType: mt, Data: data})
		}
	}
	if len(images) == 0 {
		h.writeError(w, http.StatusBadRequest, "at least one imageBase64/images entry is required")
		return
	}

	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider == "" {
		var err error
		provider, err = h.Repo.AIProvider.Get(r.Context())
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if provider == "" {
			provider = models.AIProviderGemini
		}
	}
	if provider != models.AIProviderGemini && provider != models.AIProviderOpenRouter {
		h.writeError(w, http.StatusBadRequest, "provider must be 'gemini' or 'openrouter'")
		return
	}

	var raw, model string
	if provider == models.AIProviderOpenRouter {
		apiKey, m, err := h.Repo.OpenRouter.Get(r.Context())
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if apiKey == "" {
			h.writeError(w, http.StatusBadRequest, "OpenRouter API key is not configured. Set it in Developer Settings first.")
			return
		}
		model = m
		if model == "" {
			model = defaultOpenRouterModel
		}
		raw, err = callOpenRouterChatCompletion(r.Context(), apiKey, model, images, menuParsePrompt)
		if err != nil {
			h.writeError(w, http.StatusBadGateway, "OpenRouter request failed: "+err.Error())
			return
		}
	} else {
		apiKey, m, err := h.Repo.Gemini.Get(r.Context())
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if apiKey == "" {
			h.writeError(w, http.StatusBadRequest, "Gemini API key is not configured. Set it in Developer Settings first.")
			return
		}
		model = m
		if model == "" {
			model = defaultGeminiModel
		}
		raw, err = callGeminiGenerateContentMulti(r.Context(), apiKey, model, images, menuParsePrompt)
		if err != nil {
			h.writeError(w, http.StatusBadGateway, "Gemini request failed: "+err.Error())
			return
		}
	}

	menu, parseErr := parseMenuFromAIText(raw)
	if parseErr != nil {
		// Return the raw response so the admin can inspect what the model produced.
		h.writeJSON(w, http.StatusOK, models.MenuParseResponse{
			Menu:        models.ParsedMenu{Categories: []models.ParsedMenuCategory{}},
			RawResponse: raw,
			Model:       model,
			Provider:    provider,
		})
		return
	}

	h.writeJSON(w, http.StatusOK, models.MenuParseResponse{
		Menu:        *menu,
		RawResponse: raw,
		Model:       model,
		Provider:    provider,
	})
}

// normalizeImageData strips an optional data:<mime>;base64, prefix and defaults
// the mime type to image/jpeg when missing.
func normalizeImageData(raw, mimeType string) (string, string) {
	mt := strings.TrimSpace(mimeType)
	data := raw
	if strings.HasPrefix(data, "data:") {
		if idx := strings.Index(data, ";base64,"); idx != -1 {
			if mt == "" {
				mt = data[len("data:"):idx]
			}
			data = data[idx+len(";base64,"):]
		}
	}
	if mt == "" {
		mt = "image/jpeg"
	}
	return mt, data
}

// inlineDataPart is a single inline image/PDF part sent to the AI provider.
type inlineDataPart struct {
	MimeType string
	Data     string
}

// callGeminiGenerateContent calls the Gemini generateContent endpoint with a
// single inline_data part (image/PDF) plus a text prompt, requesting JSON
// output. Kept for backward compatibility.
func callGeminiGenerateContent(ctx context.Context, apiKey, model, mimeType, base64Data, prompt string) (string, error) {
	return callGeminiGenerateContentMulti(ctx, apiKey, model, []inlineDataPart{{MimeType: mimeType, Data: base64Data}}, prompt)
}

// callGeminiGenerateContentMulti calls the Gemini generateContent endpoint with
// one or more inline_data parts (images/PDFs) plus a text prompt, requesting
// JSON output. All images are sent in a single request so Gemini can parse them
// together as one menu.
func callGeminiGenerateContentMulti(ctx context.Context, apiKey, model string, images []inlineDataPart, prompt string) (string, error) {
	// Gemini's "list models" endpoint returns names prefixed with "models/"
	// (e.g. "models/gemini-2.0-flash"). Strip it so we don't end up with a
	// doubled "models/models/..." path in the request URL.
	model = strings.TrimPrefix(model, "models/")
	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", geminiBaseURL, model, apiKey)

	totalBytes := 0
	for _, im := range images {
		totalBytes += len(im.Data)
	}
	log.Printf("[Gemini] model=%s images=%d totalBytes=%d prompt:\n%s", model, len(images), totalBytes, prompt)

	parts := []map[string]interface{}{{"text": prompt}}
	for _, im := range images {
		parts = append(parts, map[string]interface{}{
			"inline_data": map[string]interface{}{
				"mime_type": im.MimeType,
				"data":      im.Data,
			},
		})
	}

	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": parts,
			},
		},
		"generationConfig": map[string]interface{}{
			"responseMimeType": "application/json",
			"temperature":      0,
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Gemini returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	// Extract the text from candidates[0].content.parts[*].text
	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return string(respBody), nil // return raw so the admin can inspect
	}

	if len(geminiResp.Candidates) == 0 {
		if geminiResp.PromptFeedback.BlockReason != "" {
			return "", fmt.Errorf("request blocked by Gemini: %s", geminiResp.PromptFeedback.BlockReason)
		}
		return "", fmt.Errorf("Gemini returned no candidates")
	}

	var texts []string
	for _, p := range geminiResp.Candidates[0].Content.Parts {
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	raw := strings.Join(texts, "\n")
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("Gemini returned an empty response")
	}
	return raw, nil
}

// callOpenRouterChatCompletion calls the OpenRouter chat completions endpoint
// (OpenAI-compatible) with one or more image/PDF parts plus a text prompt.
// Images are sent as image_url data URLs; PDFs are sent as file parts so
// OpenRouter can run them through its file parser for supported models.
func callOpenRouterChatCompletion(ctx context.Context, apiKey, model string, images []inlineDataPart, prompt string) (string, error) {
	url := openRouterBaseURL + "/chat/completions"

	totalBytes := 0
	for _, im := range images {
		totalBytes += len(im.Data)
	}
	log.Printf("[OpenRouter] model=%s images=%d totalBytes=%d prompt:\n%s", model, len(images), totalBytes, prompt)

	content := []map[string]interface{}{{"type": "text", "text": prompt}}
	for _, im := range images {
		if im.MimeType == "application/pdf" {
			content = append(content, map[string]interface{}{
				"type": "file",
				"file": map[string]interface{}{
					"filename":  "menu.pdf",
					"file_data": "data:" + im.MimeType + ";base64," + im.Data,
				},
			})
		} else {
			content = append(content, map[string]interface{}{
				"type": "image_url",
				"image_url": map[string]interface{}{
					"url": "data:" + im.MimeType + ";base64," + im.Data,
				},
			})
		}
	}

	payload := map[string]interface{}{
		"model": model,
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": content,
			},
		},
		"temperature": 0,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("X-Title", "Mario POS - AI Menu Upload")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenRouter returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	var orResp struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &orResp); err != nil {
		return string(respBody), nil // return raw so the admin can inspect
	}

	if orResp.Error.Message != "" {
		return "", fmt.Errorf("OpenRouter error: %s", orResp.Error.Message)
	}
	if len(orResp.Choices) == 0 {
		return "", fmt.Errorf("OpenRouter returned no choices")
	}

	raw := extractOpenRouterMessageText(orResp.Choices[0].Message.Content)
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("OpenRouter returned an empty response")
	}
	return raw, nil
}

// extractOpenRouterMessageText handles the two shapes OpenAI-compatible APIs
// use for message content: a plain string, or an array of typed parts.
func extractOpenRouterMessageText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var texts []string
		for _, p := range parts {
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

// parseMenuFromAIText parses the standardized JSON menu returned by the model.
// It tolerates surrounding markdown fences and extracts the first JSON object.
func parseMenuFromAIText(raw string) (*models.ParsedMenu, error) {
	cleaned := extractJSONObject(raw)
	if cleaned == "" {
		return nil, fmt.Errorf("no JSON object found in response")
	}

	var menu models.ParsedMenu
	if err := json.Unmarshal([]byte(cleaned), &menu); err != nil {
		return nil, err
	}
	if menu.Categories == nil {
		menu.Categories = []models.ParsedMenuCategory{}
	}
	for i := range menu.Categories {
		if menu.Categories[i].Items == nil {
			menu.Categories[i].Items = []models.ParsedMenuItem{}
		}
	}
	return &menu, nil
}

// extractJSONObject finds the first '{' ... '}' balanced JSON object in the
// string, stripping any markdown code fences the model might still add.
func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	// Strip ```json or ``` fences if present.
	if strings.HasPrefix(s, "```") {
		// Remove opening fence line.
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = strings.TrimSpace(s[idx+1:])
		}
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}

	start := strings.Index(s, "{")
	if start == -1 {
		return ""
	}
	depth := 0
	inStr := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// ==========================================
// BULK MENU CREATE HANDLER
// ==========================================

// BulkCreateMenu handles POST /api/menu/bulk (superadmin only)
// It applies the provided categories and items for a store in a single
// transaction. The Mode field controls the behaviour:
//   - "add" (default):     insert all parsed categories/items.
//   - "replace":           soft-delete the entire current menu, then insert.
//   - "merge":             update matched items in place, add new items, and
//     leave untouched items as-is.
//
// The legacy ReplaceExisting bool is honoured when Mode is empty: true maps to
// "replace" and false maps to "add".
func (h *Handler) BulkCreateMenu(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if claims.Role != "superadmin" {
		h.writeError(w, http.StatusForbidden, "Access denied. Superadmin role required.")
		return
	}

	var req models.BulkMenuRequest
	if err := h.readJSON(r, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	storeID := strings.TrimSpace(req.StoreID)
	if storeID == "" {
		h.writeError(w, http.StatusBadRequest, "storeId is required")
		return
	}
	store, err := h.Repo.Store.GetByID(r.Context(), storeID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if store == nil {
		h.writeError(w, http.StatusBadRequest, "Store not found")
		return
	}
	if req.Categories == nil {
		req.Categories = []models.ParsedMenuCategory{}
	}

	// Resolve the import mode, honouring the legacy ReplaceExisting field.
	mode := req.Mode
	if mode == "" {
		if req.ReplaceExisting {
			mode = models.BulkMenuModeReplace
		} else {
			mode = models.BulkMenuModeAdd
		}
	}

	switch mode {
	case models.BulkMenuModeMerge:
		catsAdded, catsReused, itemsAdded, itemsUpdated, err := h.Repo.Menu.BulkMerge(r.Context(), storeID, req.Categories)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		h.writeJSON(w, http.StatusOK, models.BulkMenuResponse{
			Message:          "Menu merged successfully",
			CategoriesAdded:  catsAdded,
			CategoriesReused: catsReused,
			ItemsAdded:       itemsAdded,
			ItemsUpdated:     itemsUpdated,
		})
	case models.BulkMenuModeReplace:
		catsAdded, itemsAdded, err := h.Repo.Menu.BulkCreate(r.Context(), storeID, true, req.Categories)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		h.writeJSON(w, http.StatusOK, models.BulkMenuResponse{
			Message:         "Menu replaced successfully",
			CategoriesAdded: catsAdded,
			ItemsAdded:      itemsAdded,
		})
	default: // "add"
		catsAdded, itemsAdded, err := h.Repo.Menu.BulkCreate(r.Context(), storeID, false, req.Categories)
		if err != nil {
			h.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		h.writeJSON(w, http.StatusOK, models.BulkMenuResponse{
			Message:         "Menu imported successfully",
			CategoriesAdded: catsAdded,
			ItemsAdded:      itemsAdded,
		})
	}
}
