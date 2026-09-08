package server

import (
	"errors"
	"net/http"
	"strings"

	builtintools "github.com/lakernote/easy-agent/internal/builtin/tools"
	"github.com/lakernote/easy-agent/internal/store"
)

type researchProviderSettingsInput struct {
	ID          string `json:"id"`
	Endpoint    string `json:"endpoint,omitempty"`
	Secret      string `json:"secret,omitempty"`
	ClearSecret bool   `json:"clearSecret,omitempty"`
}

type researchSettingsInput struct {
	Providers []researchProviderSettingsInput `json:"providers"`
}

type researchProviderView struct {
	builtintools.ResearchProviderDefinition
	Endpoint          string `json:"endpoint,omitempty"`
	EffectiveEndpoint string `json:"effectiveEndpoint,omitempty"`
	EndpointSource    string `json:"endpointSource,omitempty"`
	SecretConfigured  bool   `json:"secretConfigured,omitempty"`
	SecretAvailable   bool   `json:"secretAvailable,omitempty"`
	SecretSource      string `json:"secretSource,omitempty"`
	Ready             bool   `json:"ready"`
}

type researchSettingsView struct {
	Providers       []researchProviderView                          `json:"providers"`
	ExecutionLayers []builtintools.ResearchExecutionLayerDefinition `json:"executionLayers"`
}

type researchProviderTestInput struct {
	ProviderID string                          `json:"providerId"`
	Providers  []researchProviderSettingsInput `json:"providers"`
}

func (server *Server) saveResearchSettings(response http.ResponseWriter, request *http.Request) {
	var input researchSettingsInput
	if !decodeJSON(response, request, &input) {
		return
	}
	current, err := server.store.GetResearchSettings()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	prepared, err := prepareResearchSettingsInput(current, input.Providers)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	if err := builtintools.ValidateResearchConfig(researchConfigFromStored(prepared)); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := server.store.SaveResearchSettings(prepared)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, publicResearchSettings(saved))
}

func (server *Server) testResearchProvider(response http.ResponseWriter, request *http.Request) {
	var input researchProviderTestInput
	if !decodeJSON(response, request, &input) {
		return
	}
	current, err := server.store.GetResearchSettings()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	prepared, err := prepareResearchSettingsInput(current, input.Providers)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	config := builtintools.MergeResearchConfig(builtintools.ResearchConfigFromEnvironment(), researchConfigFromStored(prepared))
	result, err := builtintools.TestResearchProvider(request.Context(), config, strings.TrimSpace(input.ProviderID))
	if err != nil {
		if errors.Is(err, builtintools.ErrResearchProviderConfiguration) {
			writeError(response, http.StatusBadRequest, strings.TrimPrefix(err.Error(), builtintools.ErrResearchProviderConfiguration.Error()+": "))
			return
		}
		writeError(response, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func prepareResearchSettingsInput(current store.ResearchSettings, input []researchProviderSettingsInput) (store.ResearchSettings, error) {
	if len(input) > len(builtintools.ResearchProviderDefinitions()) {
		return store.ResearchSettings{}, &inputError{message: "Research Provider 数量超过注册表范围"}
	}
	providers := make(map[string]store.ResearchProviderSettings, len(current.Providers)+len(input))
	for id, provider := range current.Providers {
		providers[id] = provider
	}
	seen := make(map[string]struct{}, len(input))
	for _, item := range input {
		id := strings.TrimSpace(item.ID)
		definition, ok := builtintools.ResearchProviderDefinitionByID(id)
		if !ok {
			return store.ResearchSettings{}, &inputError{message: "不支持的 Research Provider: " + id}
		}
		if _, duplicate := seen[id]; duplicate {
			return store.ResearchSettings{}, &inputError{message: "Research Provider 重复: " + id}
		}
		seen[id] = struct{}{}
		provider := providers[id]
		if definition.EndpointLabel == "" && strings.TrimSpace(item.Endpoint) != "" {
			return store.ResearchSettings{}, &inputError{message: definition.Name + " 不支持自定义 URL"}
		}
		if definition.SecretLabel == "" && (strings.TrimSpace(item.Secret) != "" || item.ClearSecret) {
			return store.ResearchSettings{}, &inputError{message: definition.Name + " 不支持密钥"}
		}
		endpoint := strings.TrimSpace(item.Endpoint)
		secret := strings.TrimSpace(item.Secret)
		if len(endpoint) > 2_048 {
			return store.ResearchSettings{}, &inputError{message: definition.Name + " URL 不能超过 2048 个字符"}
		}
		if len(secret) > 16_384 {
			return store.ResearchSettings{}, &inputError{message: definition.Name + " 密钥不能超过 16384 个字符"}
		}
		provider.Endpoint = endpoint
		switch {
		case item.ClearSecret:
			provider.Secret = ""
		case secret != "":
			provider.Secret = secret
		}
		if provider.Endpoint == "" && provider.Secret == "" {
			delete(providers, id)
		} else {
			providers[id] = provider
		}
	}
	return store.ResearchSettings{Providers: providers}, nil
}

// inputError keeps validation failures separate from provider/network errors.
// It intentionally contains no secret material.
type inputError struct{ message string }

func (err *inputError) Error() string { return err.message }

func researchConfigFromStored(settings store.ResearchSettings) builtintools.ResearchConfig {
	providers := make(map[string]builtintools.ResearchProviderConfig, len(settings.Providers))
	for id, provider := range settings.Providers {
		providers[id] = builtintools.ResearchProviderConfig{Endpoint: provider.Endpoint, Secret: provider.Secret}
	}
	return builtintools.ResearchConfig{Providers: providers}
}

func effectiveResearchConfig(settings store.ResearchSettings) builtintools.ResearchConfig {
	return builtintools.MergeResearchConfig(builtintools.ResearchConfigFromEnvironment(), researchConfigFromStored(settings))
}

func publicResearchSettings(settings store.ResearchSettings) researchSettingsView {
	environment := builtintools.ResearchConfigFromEnvironment()
	effective := effectiveResearchConfig(settings)
	providers := make([]researchProviderView, 0)
	for _, definition := range builtintools.ResearchProviderDefinitions() {
		stored := settings.Providers[definition.ID]
		fromEnvironment := environment.Provider(definition.ID)
		resolved := effective.Provider(definition.ID)
		view := researchProviderView{
			ResearchProviderDefinition: definition,
			Endpoint:                   stored.Endpoint,
			EffectiveEndpoint:          resolved.Endpoint,
			SecretConfigured:           stored.Secret != "",
			SecretAvailable:            resolved.Secret != "",
			Ready:                      builtintools.ResearchProviderReady(effective, definition.ID),
		}
		switch {
		case stored.Endpoint != "":
			view.EndpointSource = "saved"
		case fromEnvironment.Endpoint != "":
			view.EndpointSource = "environment"
		}
		switch {
		case stored.Secret != "":
			view.SecretSource = "saved"
		case fromEnvironment.Secret != "":
			view.SecretSource = "environment"
		}
		providers = append(providers, view)
	}
	return researchSettingsView{Providers: providers, ExecutionLayers: builtintools.ResearchExecutionLayerDefinitions()}
}
