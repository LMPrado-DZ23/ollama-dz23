package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ollama/ollama/api"
	"github.com/ollama/ollama/envconfig"
	"github.com/ollama/ollama/internal/agent"
)

type agentAPI struct {
	runtime      *agent.Runtime
	context      *agent.ContextStore
	auth         *agent.AuthStore
	authRequired bool
	push         *agent.PushService
	samlMu       sync.Mutex
	samlServices map[string]*agent.SAMLService
}

func newAgentAPI(runtime *agent.Runtime) (*agentAPI, error) {
	storeRoot := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_AUTH_STORE"))
	auth, err := agent.NewAuthStore(storeRoot)
	if err != nil {
		return nil, err
	}
	runtime.SetAuthStore(auth)
	required, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("OLLAMA_AGENT_AUTH_REQUIRED")))
	return &agentAPI{runtime: runtime, context: runtime.Context(), auth: auth, authRequired: required, push: runtime.Push(), samlServices: map[string]*agent.SAMLService{}}, nil
}

func newDefaultAgentRuntime() (*agent.Runtime, error) {
	workspaceRoot := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_ROOT"))
	if workspaceRoot == "" {
		workspaceRoot = filepath.Join(os.TempDir(), "ollama-agent-workspace")
	}
	storeRoot := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_STORE"))
	if storeRoot == "" {
		storeRoot = filepath.Join(filepath.Dir(workspaceRoot), ".ollama-agent-store")
	}
	var store agent.Store
	if databaseURL := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_DATABASE_URL")); databaseURL != "" {
		postgres, err := agent.OpenPostgresStore(context.Background(), databaseURL)
		if err != nil {
			return nil, fmt.Errorf("open agent PostgreSQL store: %w", err)
		}
		store = postgres
	} else {
		local, err := agent.NewJSONStore(storeRoot)
		if err != nil {
			return nil, err
		}
		store = local
	}
	contextStore, err := agent.NewContextStore(filepath.Join(storeRoot, "context"))
	if err != nil {
		return nil, err
	}
	if embedModel := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_EMBED_MODEL")); embedModel != "" {
		contextStore.SetEmbedder(agent.OllamaEmbedder{Client: api.NewClient(envconfig.ConnectableHost(), http.DefaultClient), Model: embedModel})
	}
	connectors, err := loadAgentConnectors()
	if err != nil {
		return nil, err
	}
	mcp, err := loadAgentMCP()
	if err != nil {
		return nil, err
	}
	media, err := loadAgentMedia()
	if err != nil {
		return nil, err
	}
	deployments, err := loadAgentDeployments()
	if err != nil {
		return nil, err
	}
	push, err := agent.NewPushService(filepath.Join(storeRoot, "push"), os.Getenv("OLLAMA_AGENT_PUSH_ENDPOINT"))
	if err != nil {
		return nil, fmt.Errorf("initialize push service: %w", err)
	}
	telemetry, err := agent.NewTelemetry(context.Background(), os.Getenv("OLLAMA_AGENT_OTLP_ENDPOINT"))
	if err != nil {
		return nil, fmt.Errorf("initialize agent OpenTelemetry: %w", err)
	}
	var redisQueue *agent.RedisQueue
	if redisURL := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_REDIS_URL")); redisURL != "" {
		redisQueue, err = agent.OpenRedisQueue(context.Background(), redisURL, os.Getenv("OLLAMA_AGENT_REDIS_PREFIX"))
		if err != nil {
			return nil, fmt.Errorf("open agent Redis queue: %w", err)
		}
	}
	var planner agent.Planner = agent.RulePlanner{}
	if model := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_MODEL")); model != "" {
		planner = agent.OllamaPlanner{
			Client:   api.NewClient(envconfig.ConnectableHost(), http.DefaultClient),
			Model:    model,
			Fallback: agent.RulePlanner{},
		}
	}
	return agent.NewRuntime(agent.RuntimeConfig{Store: store, Context: contextStore, Planner: planner, WorkspaceRoot: workspaceRoot, Connectors: connectors, MCP: mcp, Media: media, RedisQueue: redisQueue, Telemetry: telemetry, Push: push, Deployments: deployments})
}

func loadAgentConnectors() (*agent.ConnectorManager, error) {
	configPath := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_CONNECTORS"))
	if configPath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var configs []agent.ConnectorConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return nil, err
	}
	manager := agent.NewConnectorManager()
	for _, config := range configs {
		if err := manager.Register(config); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

func loadAgentDeployments() (*agent.DeploymentManager, error) {
	configPath := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_DEPLOYMENTS"))
	if configPath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var configs []agent.DeployConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return nil, err
	}
	manager := agent.NewDeploymentManager()
	for _, config := range configs {
		if err := manager.Register(config); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

func loadAgentMedia() (*agent.MediaManager, error) {
	baseURL := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_MEDIA_BASE_URL"))
	if baseURL == "" && strings.EqualFold(strings.TrimSpace(os.Getenv("OLLAMA_AGENT_MEDIA_LOCAL")), "true") {
		baseURL = "http://127.0.0.1:11434/v1"
	}
	if baseURL == "" {
		return nil, nil
	}
	return agent.NewMediaManager(agent.MediaProvider{
		Name:               "configured",
		BaseURL:            baseURL,
		APIKey:             os.Getenv("OLLAMA_AGENT_MEDIA_API_KEY"),
		ImageModel:         os.Getenv("OLLAMA_AGENT_MEDIA_IMAGE_MODEL"),
		VideoModel:         os.Getenv("OLLAMA_AGENT_MEDIA_VIDEO_MODEL"),
		SpeechModel:        os.Getenv("OLLAMA_AGENT_MEDIA_SPEECH_MODEL"),
		TranscriptionModel: os.Getenv("OLLAMA_AGENT_MEDIA_TRANSCRIPTION_MODEL"),
	})
}

func loadAgentMCP() (*agent.MCPManager, error) {
	configPath := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_MCP"))
	if configPath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var configs []agent.MCPServerConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return nil, err
	}
	manager := agent.NewMCPManager()
	for _, config := range configs {
		if err := manager.Register(config); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

func (a *agentAPI) register(r *gin.Engine) {
	group := r.Group("/api/agent/v1")
	group.Use(a.authMiddleware)
	group.GET("/health", a.health)
	group.GET("/auth/session", a.authSession)
	group.POST("/auth/dev/token", a.devToken)
	group.POST("/auth/mfa/enable", a.enableMFA)
	group.POST("/auth/mfa/disable", a.disableMFA)
	group.POST("/auth/mfa/recovery/generate", a.generateRecoveryCodes)
	group.GET("/auth/oauth/:provider/start", a.oauthStart)
	group.GET("/auth/oauth/:provider/callback", a.oauthCallback)
	group.GET("/auth/saml/:provider/start", a.samlStart)
	group.GET("/auth/saml/:provider/metadata", a.samlMetadata)
	group.POST("/auth/saml/:provider/acs", a.samlACS)
	group.POST("/notifications/register", a.registerPush)
	group.GET("/traces", a.allTraces)
	group.POST("/media/image", a.mediaImage)
	group.POST("/media/video", a.mediaVideo)
	group.POST("/media/speech", a.mediaSpeech)
	group.POST("/media/transcribe", a.mediaTranscribe)
	group.POST("/media/vision", a.mediaVision)
	group.POST("/media/ocr", a.mediaOCR)
	group.POST("/media/tone", a.mediaTone)
	group.POST("/orchestration/jobs", a.createOrchestration)
	group.GET("/orchestration/jobs/:id", a.getOrchestration)
	group.POST("/orchestration/jobs/:id/run", a.runOrchestration)
	group.POST("/orchestration/jobs/:id/cancel", a.cancelOrchestration)
	group.POST("/research", a.research)
	group.GET("/devices", a.devices)
	group.POST("/devices/pair/start", a.startDevicePairing)
	group.POST("/devices/pair/complete", a.completeDevicePairing)
	group.POST("/devices/:id/heartbeat", a.deviceHeartbeat)
	group.POST("/devices/:id/revoke", a.revokeDevice)
	group.GET("/devices/:id/connect", a.deviceConnect)
	group.POST("/projects/:id/ingest", a.ingestProject)
	group.GET("/builders", a.builders)
	group.POST("/builders", a.createBuilder)
	group.POST("/builders/:id/visual", a.updateBuilderVisual)
	group.POST("/builders/:id/undo", a.undoBuilder)
	group.POST("/builders/:id/redo", a.redoBuilder)
	group.POST("/builders/:id/preview", a.previewBuilder)
	group.POST("/builders/:id/export", a.exportBuilder)
	group.POST("/builders/:id/export/:format", a.exportProfessionalBuilder)
	group.POST("/builders/:id/publish", a.publishBuilder)
	group.GET("/deployments", a.deployments)
	group.POST("/builders/:id/deploy/:provider", a.deployBuilder)
	group.GET("/builders/:id/preview/*path", a.builderPreviewFile)
	group.GET("/metrics/prometheus", a.prometheus)
	group.GET("/tools", a.tools)
	group.GET("/connectors", a.connectors)
	group.GET("/mcp", a.mcp)
	group.GET("/jobs", a.jobs)
	group.POST("/jobs/:id/replay", a.replayJob)
	group.GET("/skills", a.skills)
	group.GET("/schedules", a.schedules)
	group.POST("/schedules", a.createSchedule)
	group.POST("/webhooks/:schedule_id", a.webhook)
	group.POST("/projects", a.createProject)
	group.GET("/projects/:id", a.getProject)
	group.POST("/projects/:id/memories", a.addMemory)
	group.GET("/projects/:id/memories", a.searchMemories)
	group.GET("/collab/:project_id", a.collabSnapshot)
	group.GET("/collab/:project_id/stream", a.collabStream)
	group.POST("/collab/:project_id/comments", a.collabComment)
	group.POST("/collab/:project_id/presence", a.collabPresence)
	group.POST("/missions", a.createMission)
	group.GET("/missions/:id", a.getMission)
	group.GET("/missions/:id/events", a.events)
	group.GET("/missions/:id/events/stream", a.eventStream)
	group.GET("/missions/:id/traces", a.traces)
	group.GET("/missions/:id/artifacts/:artifact_id", a.artifact)
	group.POST("/missions/:id/run", a.runMission)
	group.POST("/missions/:id/cancel", a.cancelMission)
	group.POST("/missions/:id/approvals/:approval_id", a.decideApproval)
}

func (a *agentAPI) authMiddleware(c *gin.Context) {
	if !a.authRequired {
		c.Next()
		return
	}
	if strings.HasSuffix(c.Request.URL.Path, "/auth/dev/token") && strings.EqualFold(strings.TrimSpace(os.Getenv("OLLAMA_AGENT_AUTH_DEV")), "true") {
		c.Next()
		return
	}
	if (strings.Contains(c.Request.URL.Path, "/auth/oauth/") || strings.Contains(c.Request.URL.Path, "/auth/saml/")) && strings.EqualFold(strings.TrimSpace(os.Getenv("OLLAMA_AGENT_AUTH_SSO_PUBLIC")), "true") {
		c.Next()
		return
	}
	if strings.HasSuffix(c.Request.URL.Path, "/connect") {
		c.Next()
		return
	}
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "bearer token is required"})
		return
	}
	user, organization, membership, err := a.auth.Authenticate(strings.TrimSpace(header[len("Bearer "):]))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
		return
	}
	if requested := strings.TrimSpace(c.GetHeader("X-Ollama-Organization")); requested != "" && requested != organization.ID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "organization scope mismatch"})
		return
	}
	if user.MFAEnabled {
		mfaCode := strings.TrimSpace(c.GetHeader("X-Ollama-MFA-Code"))
		recoveryCode := strings.TrimSpace(c.GetHeader("X-Ollama-MFA-Recovery-Code"))
		var mfaErr error
		if recoveryCode != "" {
			mfaErr = a.auth.VerifyRecoveryCode(user.ID, recoveryCode)
		} else {
			mfaErr = a.auth.VerifyMFA(user.ID, mfaCode, time.Now().UTC())
		}
		if mfaErr != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "mfa verification required"})
			return
		}
	}
	action := "read"
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		action = "execute"
	}
	if _, err := a.auth.Authorize(user.ID, organization.ID, action); err != nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	c.Set("agent.user", user)
	c.Set("agent.organization", organization)
	c.Set("agent.membership", membership)
	c.Next()
}

func (a *agentAPI) scopedRuntime(c *gin.Context) *agent.Runtime {
	if value, ok := c.Get("agent.organization"); ok {
		if organization, ok := value.(agent.Organization); ok {
			return a.runtime.WithOrganization(organization.ID)
		}
	}
	return a.runtime
}

func (a *agentAPI) missionForRequest(c *gin.Context) (agent.Mission, error) {
	return a.missionByID(c, c.Param("id"))
}

func (a *agentAPI) missionByID(c *gin.Context, id string) (agent.Mission, error) {
	mission, err := a.runtime.GetMission(strings.TrimSpace(id))
	if err != nil {
		return agent.Mission{}, err
	}
	if !a.authRequired {
		return mission, nil
	}
	value, _ := c.Get("agent.organization")
	organization, organizationOK := value.(agent.Organization)
	if !organizationOK || organization.ID == "" || mission.OrganizationID == "" || mission.OrganizationID != organization.ID {
		return agent.Mission{}, errors.New("mission is outside the active organization")
	}
	return mission, nil
}

func missionVersionMatches(c *gin.Context, mission agent.Mission) bool {
	value := strings.TrimSpace(c.GetHeader("If-Match"))
	if value == "" {
		return true
	}
	value = strings.Trim(value, "\"")
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version != mission.Version {
		c.JSON(http.StatusConflict, gin.H{"error": "mission version conflict", "current_version": mission.Version, "mission_id": mission.ID})
		return false
	}
	return true
}

func (a *agentAPI) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "runtime": "agent-v1"})
}

func (a *agentAPI) authSession(c *gin.Context) {
	if !a.authRequired {
		c.JSON(http.StatusOK, gin.H{"authenticated": false, "mode": "local"})
		return
	}
	user, _ := c.Get("agent.user")
	if value, ok := user.(agent.User); ok {
		user = value.Public()
	}
	organization, _ := c.Get("agent.organization")
	membership, _ := c.Get("agent.membership")
	c.JSON(http.StatusOK, gin.H{"authenticated": true, "user": user, "organization": organization, "membership": membership})
}

func (a *agentAPI) enableMFA(c *gin.Context) {
	value, ok := c.Get("agent.user")
	user, userOK := value.(agent.User)
	if !ok || !userOK {
		writeAgentError(c, http.StatusUnauthorized, errors.New("authenticated user is required"))
		return
	}
	var request struct {
		Secret string `json:"secret"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	updated, err := a.auth.EnableMFA(user.ID, request.Secret)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": updated.MFAEnabled, "issuer": "Ollama DZ23 Agentic", "account": updated.Email})
}

func (a *agentAPI) disableMFA(c *gin.Context) {
	value, ok := c.Get("agent.user")
	user, userOK := value.(agent.User)
	if !ok || !userOK {
		writeAgentError(c, http.StatusUnauthorized, errors.New("authenticated user is required"))
		return
	}
	updated, err := a.auth.DisableMFA(user.ID)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": updated.MFAEnabled})
}

func (a *agentAPI) generateRecoveryCodes(c *gin.Context) {
	value, ok := c.Get("agent.user")
	user, userOK := value.(agent.User)
	if !ok || !userOK {
		writeAgentError(c, http.StatusUnauthorized, errors.New("authenticated user is required"))
		return
	}
	updated, codes, err := a.auth.GenerateRecoveryCodes(user.ID)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"user": updated.Public(), "recovery_codes": codes, "warning": "store these codes securely; they are shown only once"})
}

func (a *agentAPI) registerPush(c *gin.Context) {
	if a.push == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "push service is not configured"})
		return
	}
	var request struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	user, _ := c.Get("agent.user")
	organization, _ := c.Get("agent.organization")
	userValue, userOK := user.(agent.User)
	organizationValue, organizationOK := organization.(agent.Organization)
	if !userOK || !organizationOK {
		writeAgentError(c, http.StatusUnauthorized, errors.New("authenticated user and organization are required"))
		return
	}
	subscription, err := a.push.Register(request.Token, request.Platform, userValue.ID, organizationValue.ID)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	subscription.Token = "redacted"
	c.JSON(http.StatusCreated, subscription)
}

func (a *agentAPI) devToken(c *gin.Context) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("OLLAMA_AGENT_AUTH_DEV")), "true") {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	var input struct {
		Email        string `json:"email"`
		Name         string `json:"name"`
		Organization string `json:"organization"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	user, err := a.auth.CreateUser(input.Email, input.Name)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	organization, _, err := a.auth.CreateOrganization(input.Organization, user)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	raw, token, err := a.auth.IssueToken(user.ID, organization.ID, 24*time.Hour)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"access_token": raw, "token": token, "user": user.Public(), "organization": organization})
}

func oauthProviderFromEnv(name string) agent.OAuthProvider {
	key := strings.ToUpper(strings.NewReplacer("-", "_", " ", "_").Replace(strings.TrimSpace(name)))
	prefix := "OLLAMA_AGENT_OAUTH_" + key
	return agent.OAuthProvider{Name: name, AuthorizeURL: os.Getenv(prefix + "_AUTHORIZE_URL"), TokenURL: os.Getenv(prefix + "_TOKEN_URL"), UserInfoURL: os.Getenv(prefix + "_USERINFO_URL"), IssuerURL: os.Getenv(prefix + "_ISSUER_URL"), Audience: os.Getenv(prefix + "_AUDIENCE"), ClientIDEnv: os.Getenv(prefix + "_CLIENT_ID_ENV"), SecretEnv: os.Getenv(prefix + "_CLIENT_SECRET_ENV")}
}

func prepareOIDCProvider(ctx context.Context, provider agent.OAuthProvider) (agent.OAuthProvider, error) {
	if strings.TrimSpace(provider.IssuerURL) == "" {
		return provider, provider.Validate()
	}
	discovery, err := provider.Discover(ctx, http.DefaultClient)
	if err != nil {
		return agent.OAuthProvider{}, err
	}
	if provider.AuthorizeURL == "" {
		provider.AuthorizeURL = discovery.AuthorizationEndpoint
	}
	if provider.TokenURL == "" {
		provider.TokenURL = discovery.TokenEndpoint
	}
	if provider.UserInfoURL == "" {
		provider.UserInfoURL = discovery.UserInfoEndpoint
	}
	return provider, provider.Validate()
}

func samlProviderFromEnv(name string) agent.SAMLProviderConfig {
	key := strings.ToUpper(strings.NewReplacer("-", "_", " ", "_").Replace(strings.TrimSpace(name)))
	prefix := "OLLAMA_AGENT_SAML_" + key
	return agent.SAMLProviderConfig{
		Name:               name,
		EntityID:           os.Getenv(prefix + "_ENTITY_ID"),
		IDPMetadataURL:     os.Getenv(prefix + "_IDP_METADATA_URL"),
		MetadataURL:        os.Getenv(prefix + "_METADATA_URL"),
		ACSURL:             os.Getenv(prefix + "_ACS_URL"),
		SPPrivateKeyFile:   os.Getenv(prefix + "_SP_PRIVATE_KEY_FILE"),
		SPCertificateFile:  os.Getenv(prefix + "_SP_CERTIFICATE_FILE"),
		DefaultRedirectURI: os.Getenv(prefix + "_DEFAULT_REDIRECT_URI"),
		AllowIDPInitiated:  strings.EqualFold(os.Getenv(prefix+"_ALLOW_IDP_INITIATED"), "true"),
	}
}

func loadSAMLService(ctx context.Context, name string) (*agent.SAMLService, error) {
	config := samlProviderFromEnv(name)
	return agent.NewSAMLService(ctx, config, http.DefaultClient)
}

func (a *agentAPI) samlService(ctx context.Context, name string) (*agent.SAMLService, error) {
	a.samlMu.Lock()
	defer a.samlMu.Unlock()
	if service, ok := a.samlServices[name]; ok {
		return service, nil
	}
	service, err := loadSAMLService(ctx, name)
	if err != nil {
		return nil, err
	}
	a.samlServices[name] = service
	return service, nil
}

func (a *agentAPI) oauthStart(c *gin.Context) {
	provider, err := prepareOIDCProvider(c.Request.Context(), oauthProviderFromEnv(c.Param("provider")))
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	redirectURI := strings.TrimSpace(c.Query("redirect_uri"))
	verifier := strings.TrimSpace(c.Query("code_verifier"))
	if redirectURI == "" || verifier == "" {
		writeAgentError(c, http.StatusBadRequest, errors.New("redirect_uri and PKCE code_verifier are required"))
		return
	}
	userID := ""
	if value, ok := c.Get("agent.user"); ok {
		if user, ok := value.(agent.User); ok {
			userID = user.ID
		}
	}
	nonce := fmt.Sprintf("%x", sha256.Sum256([]byte(provider.Name+"|"+redirectURI+"|"+verifier+"|"+time.Now().UTC().String())))
	state, _, err := a.auth.CreateOAuthStateWithNonce(provider.Name, redirectURI, verifier, nonce, userID, 5*time.Minute)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	scopes := strings.Fields(c.Query("scope"))
	if len(scopes) == 0 {
		scopes = []string{"openid", "email"}
	}
	authorizationURL, err := provider.AuthorizationURLWithNonce(state, nonce, scopes)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"provider": provider.Name, "authorization_url": authorizationURL, "state": state, "expires_in": 300})
}

func (a *agentAPI) oauthCallback(c *gin.Context) {
	provider, err := prepareOIDCProvider(c.Request.Context(), oauthProviderFromEnv(c.Param("provider")))
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	redirectURI := strings.TrimSpace(c.Query("redirect_uri"))
	code := strings.TrimSpace(c.Query("code"))
	stateValue := strings.TrimSpace(c.Query("state"))
	if code == "" || stateValue == "" || redirectURI == "" {
		writeAgentError(c, http.StatusBadRequest, errors.New("code, state and redirect_uri are required"))
		return
	}
	state, err := a.auth.ConsumeOAuthState(stateValue, provider.Name, redirectURI)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	payload, err := provider.ExchangeCode(c.Request.Context(), http.DefaultClient, code, redirectURI, state.CodeVerifier)
	if err != nil {
		writeAgentError(c, http.StatusBadGateway, err)
		return
	}
	if provider.IssuerURL != "" {
		idToken, _ := payload["id_token"].(string)
		if idToken == "" {
			writeAgentError(c, http.StatusBadGateway, errors.New("oidc token response has no id_token"))
			return
		}
		claims, validationErr := provider.ValidateIDToken(c.Request.Context(), http.DefaultClient, idToken, state.Nonce)
		if validationErr != nil {
			writeAgentError(c, http.StatusBadGateway, validationErr)
			return
		}
		payload["id_token_claims"] = claims
	}
	userID := state.UserID
	if userID == "" && (provider.UserInfoURL != "" || provider.IssuerURL != "") {
		accessToken, _ := payload["access_token"].(string)
		userinfo, userinfoErr := provider.FetchUserInfo(c.Request.Context(), http.DefaultClient, accessToken)
		if userinfoErr != nil {
			writeAgentError(c, http.StatusBadGateway, userinfoErr)
			return
		}
		payload["userinfo"] = userinfo
	}
	if userID == "" {
		user, _, _, provisionErr := a.auth.ProvisionOAuthUser(payload, provider.Name)
		if provisionErr != nil {
			writeAgentError(c, http.StatusBadRequest, provisionErr)
			return
		}
		userID = user.ID
	}
	organization, _, err := a.auth.FirstOrganization(userID)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, errors.New("OAuth user has no organization"))
		return
	}
	credential, err := a.auth.StoreOAuthCredential(provider.Name, userID, organization.ID, payload)
	if err != nil {
		writeAgentError(c, http.StatusInternalServerError, err)
		return
	}
	localToken, session, err := a.auth.IssueToken(userID, organization.ID, 24*time.Hour)
	if err != nil {
		writeAgentError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"access_token": localToken, "token": session, "credential_id": credential.ID, "provider": provider.Name, "organization": organization, "expires_at": credential.ExpiresAt})
}

func (a *agentAPI) samlStart(c *gin.Context) {
	service, err := a.samlService(c.Request.Context(), c.Param("provider"))
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	redirect, relay, err := service.Start(c.Query("redirect_uri"))
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"provider": service.Provider.Name, "authorization_url": redirect, "relay_state": relay, "expires_in": 300})
}

func (a *agentAPI) samlMetadata(c *gin.Context) {
	service, err := a.samlService(c.Request.Context(), c.Param("provider"))
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	service.Metadata(c.Writer, c.Request)
}

func (a *agentAPI) samlACS(c *gin.Context) {
	service, err := a.samlService(c.Request.Context(), c.Param("provider"))
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	claims, err := service.ParseACS(c.Request)
	if err != nil {
		writeAgentError(c, http.StatusUnauthorized, err)
		return
	}
	user, organization, _, err := a.auth.ProvisionOAuthUser(claims, service.Provider.Name)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	localToken, session, err := a.auth.IssueToken(user.ID, organization.ID, 24*time.Hour)
	if err != nil {
		writeAgentError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"access_token": localToken, "token": session, "provider": service.Provider.Name, "organization": organization, "user": user.Public()})
}

func (a *agentAPI) metrics(c *gin.Context) {
	c.JSON(http.StatusOK, a.runtime.Metrics())
}

func (a *agentAPI) prometheus(c *gin.Context) {
	c.Data(http.StatusOK, "text/plain; version=0.0.4", []byte(a.runtime.Metrics().Prometheus()))
}

func (a *agentAPI) connectors(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"connectors": a.runtime.Connectors()})
}

func (a *agentAPI) mcp(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"servers": a.runtime.MCPServers()})
}

func (a *agentAPI) jobs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"jobs": a.runtime.QueueJobs(agent.QueueStatus(c.Query("status")))})
}

func (a *agentAPI) replayJob(c *gin.Context) {
	job, err := a.runtime.ReplayJob(c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusAccepted, job)
}

func (a *agentAPI) tools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": a.runtime.ListTools()})
}

func (a *agentAPI) skills(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"skills": a.context.Skills()})
}

func (a *agentAPI) schedules(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"schedules": a.context.ListSchedules()})
}

func (a *agentAPI) createSchedule(c *gin.Context) {
	var schedule agent.Schedule
	if err := decodeJSON(c, &schedule); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	created, err := a.context.CreateSchedule(schedule)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (a *agentAPI) webhook(c *gin.Context) {
	schedule, err := a.context.GetSchedule(c.Param("schedule_id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	secret := strings.TrimSpace(schedule.WebhookSecretEnv)
	if secret == "" || !verifyAgentWebhook(os.Getenv(secret), c.GetHeader("X-Ollama-Agent-Secret")) {
		writeAgentError(c, http.StatusUnauthorized, errors.New("webhook secret is invalid"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	objective := schedule.Objective + "\nWebhook payload:\n" + string(payload)
	mission, err := a.runtime.CreateMission(c.Request.Context(), agent.CreateMissionRequest{Objective: objective, Model: schedule.Model, Workspace: schedule.Workspace, ProjectID: schedule.ProjectID, AutoRun: true})
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusAccepted, mission)
}

func verifyAgentWebhook(expected, provided string) bool {
	if expected == "" || provided == "" {
		return false
	}
	expectedSum := sha256.Sum256([]byte(expected))
	providedSum := sha256.Sum256([]byte(provided))
	return hmac.Equal(expectedSum[:], providedSum[:])
}

func (a *agentAPI) createProject(c *gin.Context) {
	var request struct {
		Name string `json:"name"`
		Root string `json:"root,omitempty"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	project, err := a.context.CreateProject(request.Name, request.Root)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, project)
}

func (a *agentAPI) getProject(c *gin.Context) {
	project, err := a.context.GetProject(c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusOK, project)
}

func (a *agentAPI) collabActor(c *gin.Context) string {
	if user, ok := c.Get("agent.user"); ok {
		if actor, ok := user.(agent.User); ok && actor.ID != "" {
			return actor.ID
		}
	}
	if value := strings.TrimSpace(c.GetHeader("X-Ollama-User")); value != "" {
		return value
	}
	return "local"
}

func (a *agentAPI) collabSnapshot(c *gin.Context) {
	c.JSON(http.StatusOK, a.runtime.Collaboration().Snapshot(c.Param("project_id")))
}

func (a *agentAPI) collabComment(c *gin.Context) {
	var request struct {
		Body string `json:"body"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	comment, err := a.runtime.Collaboration().AddComment(c.Param("project_id"), a.collabActor(c), request.Body)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, comment)
}

func (a *agentAPI) collabPresence(c *gin.Context) {
	var request struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	presence, err := a.runtime.Collaboration().SetPresence(c.Param("project_id"), a.collabActor(c), request.Status)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, presence)
}

func (a *agentAPI) collabStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.Status(http.StatusNotImplemented)
		return
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		payload, _ := json.Marshal(a.runtime.Collaboration().Snapshot(c.Param("project_id")))
		_, _ = fmt.Fprintf(c.Writer, "event: collaboration\ndata: %s\n\n", payload)
		flusher.Flush()
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *agentAPI) addMemory(c *gin.Context) {
	var memory agent.Memory
	if err := decodeJSON(c, &memory); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	memory.ProjectID = c.Param("id")
	created, err := a.context.AddMemoryContext(c.Request.Context(), memory)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (a *agentAPI) searchMemories(c *gin.Context) {
	memories, err := a.context.SearchMemoriesContext(c.Request.Context(), c.Param("id"), c.Query("q"), 20)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"project_id": c.Param("id"), "memories": memories})
}

func (a *agentAPI) createMission(c *gin.Context) {
	var request agent.CreateMissionRequest
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	if value, ok := c.Get("agent.organization"); ok {
		if organization, ok := value.(agent.Organization); ok {
			request.OrganizationID = organization.ID
		}
	}
	mission, err := a.scopedRuntime(c).CreateMission(c.Request.Context(), request)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, mission)
}

func (a *agentAPI) getMission(c *gin.Context) {
	mission, err := a.missionForRequest(c)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusOK, mission)
}

func (a *agentAPI) events(c *gin.Context) {
	if _, err := a.missionForRequest(c); err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	events, err := a.scopedRuntime(c).Events(c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"mission_id": c.Param("id"), "events": events})
}

func (a *agentAPI) eventStream(c *gin.Context) {
	if _, err := a.missionForRequest(c); err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.Status(http.StatusNotImplemented)
		return
	}
	lastCount := 0
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	for {
		events, err := a.scopedRuntime(c).Events(c.Param("id"))
		if err != nil {
			return
		}
		if len(events) > lastCount {
			payload, _ := json.Marshal(events[lastCount:])
			_, _ = fmt.Fprintf(c.Writer, "event: mission\ndata: %s\n\n", payload)
			flusher.Flush()
			lastCount = len(events)
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *agentAPI) traces(c *gin.Context) {
	if _, err := a.missionForRequest(c); err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"mission_id": c.Param("id"), "spans": a.runtime.Traces("tr_" + c.Param("id"))})
}

func (a *agentAPI) allTraces(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"spans": a.runtime.Traces(c.Query("trace_id"))})
}

func (a *agentAPI) createOrchestration(c *gin.Context) {
	var request struct {
		Objective string            `json:"objective"`
		Workspace string            `json:"workspace,omitempty"`
		ProjectID string            `json:"project_id,omitempty"`
		Roles     []agent.AgentRole `json:"roles,omitempty"`
		Budget    agent.AgentBudget `json:"budget,omitempty"`
		AutoRun   bool              `json:"auto_run,omitempty"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	job, err := a.runtime.Orchestrator().Plan(request.Objective, request.Workspace, request.ProjectID, request.Roles, request.Budget)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	if request.AutoRun {
		go func(id string) { _, _ = a.runtime.Orchestrator().Run(context.Background(), id) }(job.ID)
		c.JSON(http.StatusAccepted, job)
		return
	}
	c.JSON(http.StatusCreated, job)
}

func (a *agentAPI) getOrchestration(c *gin.Context) {
	job, err := a.runtime.Orchestrator().Get(c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusOK, job)
}

func (a *agentAPI) runOrchestration(c *gin.Context) {
	job, err := a.runtime.Orchestrator().Get(c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	go func(id string) { _, _ = a.runtime.Orchestrator().Run(context.Background(), id) }(job.ID)
	c.JSON(http.StatusAccepted, gin.H{"id": job.ID, "state": agent.OrchestrationRunning})
}

func (a *agentAPI) cancelOrchestration(c *gin.Context) {
	job, err := a.runtime.Orchestrator().Cancel(c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusOK, job)
}

func (a *agentAPI) research(c *gin.Context) {
	var request agent.ResearchRequest
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	report, err := a.runtime.Research().Research(c.Request.Context(), request)
	if err != nil {
		writeAgentError(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

func (a *agentAPI) actorIdentity(c *gin.Context) (string, string) {
	if value, ok := c.Get("agent.user"); ok {
		if user, ok := value.(agent.User); ok {
			return user.ID, ""
		}
	}
	return strings.TrimSpace(c.GetHeader("X-Ollama-User")), strings.TrimSpace(c.GetHeader("X-Ollama-Organization"))
}

func (a *agentAPI) devices(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"devices": a.runtime.Devices().List()})
}

func (a *agentAPI) startDevicePairing(c *gin.Context) {
	userID, organizationID := a.actorIdentity(c)
	code, pairing, err := a.runtime.Devices().StartPairing(userID, organizationID, 5*time.Minute)
	if err != nil {
		writeAgentError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"pairing_code": code, "expires_at": pairing.ExpiresAt})
}

func (a *agentAPI) completeDevicePairing(c *gin.Context) {
	var request struct {
		Code         string                   `json:"pairing_code"`
		Name         string                   `json:"name"`
		Platform     string                   `json:"platform"`
		Capabilities []agent.DeviceCapability `json:"capabilities"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	userID, organizationID := a.actorIdentity(c)
	device, token, err := a.runtime.Devices().CompletePairing(request.Code, request.Name, request.Platform, userID, organizationID, request.Capabilities)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"device": device, "device_token": token})
}

func (a *agentAPI) deviceHeartbeat(c *gin.Context) {
	var request struct {
		Capabilities []agent.DeviceCapability `json:"capabilities,omitempty"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	if token == "" {
		token = strings.TrimSpace(c.GetHeader("X-Device-Token"))
	}
	device, err := a.runtime.Devices().Heartbeat(c.Param("id"), token, request.Capabilities)
	if err != nil {
		writeAgentError(c, http.StatusUnauthorized, err)
		return
	}
	c.JSON(http.StatusOK, device)
}

func (a *agentAPI) revokeDevice(c *gin.Context) {
	device, err := a.runtime.Devices().Revoke(c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusOK, device)
}

func (a *agentAPI) ingestProject(c *gin.Context) {
	var request agent.DocumentIngestRequest
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	request.ProjectID = c.Param("id")
	project, err := a.runtime.Context().GetProject(request.ProjectID)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	request.Workspace = project.Root
	memories, err := a.runtime.Ingestion().Ingest(c.Request.Context(), request)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"project_id": request.ProjectID, "memories": memories, "count": len(memories)})
}

func (a *agentAPI) mediaWorkspace(c *gin.Context, missionID string) (agent.Mission, string, error) {
	mission, err := a.missionByID(c, missionID)
	if err != nil {
		return agent.Mission{}, "", err
	}
	if strings.TrimSpace(mission.Workspace) == "" {
		return agent.Mission{}, "", errors.New("mission workspace is required")
	}
	return mission, mission.Workspace, nil
}

func (a *agentAPI) mediaImage(c *gin.Context) {
	var request struct {
		MissionID string `json:"mission_id"`
		Prompt    string `json:"prompt"`
		Model     string `json:"model"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	if a.runtime.Media() == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "media provider is not configured"})
		return
	}
	mission, workspace, err := a.mediaWorkspace(c, request.MissionID)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	result, err := a.runtime.Media().GenerateImage(c.Request.Context(), workspace, request.Prompt, request.Model)
	if err != nil {
		writeAgentError(c, http.StatusBadGateway, err)
		return
	}
	result.Artifact.MissionID = mission.ID
	c.JSON(http.StatusCreated, result)
}

func (a *agentAPI) mediaVideo(c *gin.Context) {
	var request struct {
		MissionID string `json:"mission_id"`
		Prompt    string `json:"prompt"`
		Model     string `json:"model"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	if a.runtime.Media() == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "media provider is not configured"})
		return
	}
	mission, workspace, err := a.mediaWorkspace(c, request.MissionID)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	result, err := a.runtime.Media().GenerateVideo(c.Request.Context(), workspace, request.Prompt, request.Model)
	if err != nil {
		writeAgentError(c, http.StatusBadGateway, err)
		return
	}
	result.Artifact.MissionID = mission.ID
	c.JSON(http.StatusCreated, result)
}

func (a *agentAPI) mediaSpeech(c *gin.Context) {
	var request struct {
		MissionID string `json:"mission_id"`
		Text      string `json:"text"`
		Voice     string `json:"voice"`
		Model     string `json:"model"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	if a.runtime.Media() == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "media provider is not configured"})
		return
	}
	mission, workspace, err := a.mediaWorkspace(c, request.MissionID)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	result, err := a.runtime.Media().GenerateSpeech(c.Request.Context(), workspace, request.Text, request.Voice, request.Model)
	if err != nil {
		writeAgentError(c, http.StatusBadGateway, err)
		return
	}
	result.Artifact.MissionID = mission.ID
	c.JSON(http.StatusCreated, result)
}

func (a *agentAPI) mediaTranscribe(c *gin.Context) {
	var request struct {
		MissionID string `json:"mission_id"`
		InputPath string `json:"input_path"`
		Model     string `json:"model"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	if a.runtime.Media() == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "media provider is not configured"})
		return
	}
	mission, workspace, err := a.mediaWorkspace(c, request.MissionID)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	inputPath, err := containedPath(workspace, request.InputPath)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	result, err := a.runtime.Media().Transcribe(c.Request.Context(), workspace, inputPath, request.Model)
	if err != nil {
		writeAgentError(c, http.StatusBadGateway, err)
		return
	}
	result.Artifact.MissionID = mission.ID
	c.JSON(http.StatusCreated, result)
}

func (a *agentAPI) mediaVision(c *gin.Context) {
	var request struct {
		MissionID string `json:"mission_id"`
		InputPath string `json:"input_path"`
		Prompt    string `json:"prompt"`
		Model     string `json:"model"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	if a.runtime.Media() == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "media provider is not configured"})
		return
	}
	mission, workspace, err := a.mediaWorkspace(c, request.MissionID)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	inputPath, err := containedPath(workspace, request.InputPath)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	result, err := a.runtime.Media().AnalyzeImage(c.Request.Context(), workspace, inputPath, request.Prompt, request.Model)
	if err != nil {
		writeAgentError(c, http.StatusBadGateway, err)
		return
	}
	result.Artifact.MissionID = mission.ID
	c.JSON(http.StatusCreated, result)
}

func (a *agentAPI) mediaOCR(c *gin.Context) {
	var request struct {
		MissionID string `json:"mission_id"`
		InputPath string `json:"input_path"`
		Language  string `json:"language"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	mission, workspace, err := a.mediaWorkspace(c, request.MissionID)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	inputPath, err := containedPath(workspace, request.InputPath)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	result, err := agent.OCRLocal(c.Request.Context(), workspace, inputPath, request.Language)
	if err != nil {
		writeAgentError(c, http.StatusBadGateway, err)
		return
	}
	result.Artifact.MissionID = mission.ID
	c.JSON(http.StatusCreated, result)
}

func (a *agentAPI) mediaTone(c *gin.Context) {
	var request struct {
		MissionID  string  `json:"mission_id"`
		Frequency  float64 `json:"frequency"`
		DurationMS int     `json:"duration_ms"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	mission, workspace, err := a.mediaWorkspace(c, request.MissionID)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	result, err := agent.GenerateTone(workspace, request.Frequency, time.Duration(request.DurationMS)*time.Millisecond)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	result.Artifact.MissionID = mission.ID
	c.JSON(http.StatusCreated, result)
}

func (a *agentAPI) builders(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"projects": a.runtime.Builder().List()})
}

func (a *agentAPI) createBuilder(c *gin.Context) {
	var spec agent.BuilderSpec
	if err := decodeJSON(c, &spec); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	project, err := a.runtime.Builder().Create(c.Request.Context(), spec)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, project)
}

func (a *agentAPI) previewBuilder(c *gin.Context) {
	project, artifact, err := a.runtime.Builder().Preview(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"project": project, "artifact": artifact})
}

func (a *agentAPI) updateBuilderVisual(c *gin.Context) {
	var request struct {
		Components []agent.VisualComponent `json:"components"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	project, err := a.runtime.Builder().ApplyVisualComponents(c.Request.Context(), c.Param("id"), request.Components)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusOK, project)
}

func (a *agentAPI) undoBuilder(c *gin.Context) {
	project, err := a.runtime.Builder().Undo(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusOK, project)
}

func (a *agentAPI) redoBuilder(c *gin.Context) {
	project, err := a.runtime.Builder().Redo(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusOK, project)
}

func (a *agentAPI) exportBuilder(c *gin.Context) {
	project, archivePath, err := a.runtime.Builder().Export(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"project": project, "archive_path": archivePath})
}

func (a *agentAPI) exportProfessionalBuilder(c *gin.Context) {
	project, outputPath, err := a.runtime.Builder().ExportProfessional(c.Request.Context(), c.Param("id"), c.Param("format"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"project": project, "format": c.Param("format"), "output_path": outputPath})
}

func (a *agentAPI) publishBuilder(c *gin.Context) {
	project, publishedPath, err := a.runtime.Builder().PublishLocal(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"project": project, "published_path": publishedPath})
}

func (a *agentAPI) deployments(c *gin.Context) {
	manager := a.runtime.Deployments()
	if manager == nil {
		c.JSON(http.StatusOK, gin.H{"providers": []agent.DeployConfig{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"providers": manager.List()})
}

func (a *agentAPI) deployBuilder(c *gin.Context) {
	manager := a.runtime.Deployments()
	if manager == nil {
		writeAgentError(c, http.StatusNotImplemented, errors.New("no deployment providers are configured"))
		return
	}
	var request struct {
		Target   string `json:"target,omitempty"`
		Approved bool   `json:"approved"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	if !request.Approved {
		writeAgentError(c, http.StatusPreconditionRequired, errors.New("external deployment requires explicit approval"))
		return
	}
	project, err := a.runtime.Builder().Get(c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	result, err := manager.Deploy(c.Request.Context(), c.Param("provider"), agent.DeploymentRequest{Name: project.Name, Root: project.Root, Target: request.Target})
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"project": project, "deployment": result})
}

func (a *agentAPI) builderPreviewFile(c *gin.Context) {
	project, err := a.runtime.Builder().Get(c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	relative := strings.TrimPrefix(c.Param("path"), "/")
	if relative == "" {
		relative = project.Entry
	}
	path, err := containedPath(project.Root, relative)
	if err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		writeAgentError(c, http.StatusNotFound, errors.New("preview file not found"))
		return
	}
	c.File(path)
}

func containedPath(root, requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return "", errors.New("input_path is required")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootAbs, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbs, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", errors.New("input_path escapes mission workspace")
	}
	return candidate, nil
}

func (a *agentAPI) artifact(c *gin.Context) {
	if _, err := a.missionForRequest(c); err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	manifest, path, err := a.scopedRuntime(c).Artifact(c.Param("id"), c.Param("artifact_id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.Header("X-Artifact-SHA256", manifest.SHA256)
	c.FileAttachment(path, manifest.Name)
}

func (a *agentAPI) runMission(c *gin.Context) {
	id := c.Param("id")
	mission, err := a.missionForRequest(c)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	if !missionVersionMatches(c, mission) {
		return
	}
	job, err := a.scopedRuntime(c).EnqueueMission(id)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"mission_id": id, "state": agent.MissionRunning, "job": job})
}

func (a *agentAPI) cancelMission(c *gin.Context) {
	mission, err := a.missionForRequest(c)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	if !missionVersionMatches(c, mission) {
		return
	}
	mission, err = a.scopedRuntime(c).Cancel(c.Param("id"))
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusOK, mission)
}

func (a *agentAPI) decideApproval(c *gin.Context) {
	var request struct {
		Approved bool   `json:"approved"`
		Reason   string `json:"reason,omitempty"`
	}
	if err := decodeJSON(c, &request); err != nil {
		writeAgentError(c, http.StatusBadRequest, err)
		return
	}
	mission, err := a.missionForRequest(c)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	if !missionVersionMatches(c, mission) {
		return
	}
	mission, err = a.scopedRuntime(c).DecideApproval(c.Param("id"), c.Param("approval_id"), request.Approved, request.Reason)
	if err != nil {
		writeAgentError(c, statusForAgentError(err), err)
		return
	}
	c.JSON(http.StatusOK, mission)
}

func decodeJSON(c *gin.Context, value any) error {
	if c.Request.Body == nil {
		return errors.New("request body is required")
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func writeAgentError(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{"error": strings.TrimSpace(err.Error())})
}

func statusForAgentError(err error) int {
	if errors.Is(err, os.ErrNotExist) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}
