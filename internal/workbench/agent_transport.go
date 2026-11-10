package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/host"
)

// AgentTarget separates the desktop's client configuration from its selected
// workload. A selected workload always requires an explicit generation.
type AgentTarget struct {
	Scope      string `json:"scope"`
	Connection string `json:"connection_id,omitempty"`
}

func agentConnectTarget(ctx context.Context, configPath string, target AgentTarget) (*controllerClient, error) {
	if target.Scope == "" || target.Scope == "local" {
		return agentConnect(ctx, configPath)
	}
	if target.Scope != "desktop" && target.Scope != "selected" && target.Scope != "client" {
		return nil, &AgentError{Code: "invalid_input", Message: "scope must be local, desktop, selected or client.", NextAction: "capabilities"}
	}
	if target.Scope == "selected" && target.Connection == "" {
		return nil, &AgentError{Code: "invalid_input", Message: "selected scope requires --connection from client.context.", NextAction: "client.context"}
	}
	path := desktopConfigPath(configPath)
	if target.Scope == "client" {
		path = configPath
	}
	c, err := connectController(filepath.Dir(path))
	if err != nil {
		return nil, &AgentError{Code: "service_unavailable", Message: "Open the Cage desktop before managing client configuration.", NextAction: "open_desktop"}
	}
	want, err := currentController()
	if err != nil || !want.matches(c.launch.Controller) || (target.Scope != "client" && !c.launch.DesktopGateway) || (target.Scope == "client" && !c.launch.ClientOnly) || filepath.Clean(c.launch.ConfigPath) != filepath.Clean(path) {
		c.close()
		return nil, &AgentError{Code: "controller_mismatch", Message: "The desktop controller does not match this CLI and configuration.", NextAction: "open_desktop"}
	}
	u, _ := url.Parse(c.launch.URL)
	code, err := c.callContext(ctx, "POST", "/api/session", map[string]string{"token": u.Fragment}, nil)
	if err != nil || code != 200 {
		c.close()
		return nil, agentHTTPError(401)
	}
	c.client.Jar.SetCookies(u, []*http.Cookie{{Name: cookieName + "_" + u.Port() + "-desktop", Value: trustedViewProof(c.record.Token), Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode}})
	return c, nil
}

type AgentResponse struct {
	HTTPStatus  int             `json:"http_status"`
	ContentType string          `json:"content_type,omitempty"`
	Recovery    string          `json:"recovery,omitempty"`
	Data        json.RawMessage `json:"body,omitempty"`
	Path        string          `json:"path,omitempty"`
}

// AgentRequest accepts the same HTTP paths and payloads as the workbench. The
// URL is relative to an authenticated, pinned controller, never caller-owned.
// Binary uploads/downloads use streams; JSON results preserve business values,
// including secrets explicitly requested using the UI's reauthentication.
func AgentRequest(ctx context.Context, configPath string, target AgentTarget, method, path string, input io.Reader, contentType, output string) (AgentResponse, error) {
	var result AgentResponse
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/api/") || u.Fragment != "" || strings.ContainsAny(path, "\r\n\\") || strings.Contains(u.Path, "//") || strings.Contains(u.Path, "/../") {
		return result, &AgentError{Code: "invalid_input", Message: "Use a relative /api/ path without a host, fragment or traversal.", NextAction: "capabilities"}
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
	default:
		return result, &AgentError{Code: "invalid_input", Message: "Unsupported HTTP method.", NextAction: "capabilities"}
	}
	// These endpoints are internal transports, not user actions. Device
	// enrollment/unlock must run inside the same native credential store and
	// biometric policy as the desktop; never return its wrapping secret.
	if u.Path == "/api/agent/native/window" || u.Path == "/api/account/device-enroll" || u.Path == "/api/account/device-unlock" || u.Path == "/api/account/device-revoke" || u.Path == "/api/session" || u.Path == "/api/launch" || u.Path == "/api/handoff" || u.Path == "/api/desktop-owner" || strings.HasPrefix(u.Path, "/api/desktop-local/") {
		return result, &AgentError{Code: "native_transport_required", Message: "Use native.submit with the same accountDevice or desktop action. Transport credentials are not exported.", NextAction: "native.submit"}
	}
	// Client-only operations must address the desktop explicitly. Ordinary
	// business routes on that listener require the selected target's generation.
	clientPath := strings.HasPrefix(u.Path, "/api/client/") || strings.HasPrefix(u.Path, "/api/agent/native") || u.Path == "/api/agent/routes"
	if target.Scope == "client" && !clientPath && target.Connection == "" {
		return result, &AgentError{Code: "invalid_input", Message: "Client workload requests require the explicit selected --connection", NextAction: "client.context"}
	}
	if target.Scope == "desktop" && !clientPath {
		return result, &AgentError{Code: "invalid_input", Message: "Use selected scope with --connection for workload requests through the desktop.", NextAction: "client.context"}
	}
	if output != "" {
		if !filepath.IsAbs(output) {
			return result, &AgentError{Code: "invalid_input", Message: "output must be an absolute new file path", NextAction: "capabilities"}
		}
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			return result, &AgentError{Code: "invalid_input", Message: "Existing output files are preserved", NextAction: "capabilities"}
		}
	}
	c, err := agentConnectTarget(ctx, configPath, target)
	if err != nil {
		return result, err
	}
	defer c.close()
	c.client.Timeout = 2 * time.Minute
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, input)
	if err != nil {
		return result, err
	}
	req.Header.Set("Origin", c.origin)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	connection := "local"
	if target.Connection != "" {
		connection = target.Connection
	}
	req.Header.Set(connectionHeader, connection)
	response, err := c.client.Do(req)
	if err != nil {
		return result, &AgentError{Code: "outcome_unknown", Message: "The request was interrupted. Inspect the original target before repeating a mutation.", NextAction: "status"}
	}
	defer response.Body.Close()
	result.HTTPStatus = response.StatusCode
	result.ContentType = response.Header.Get("Content-Type")
	result.Recovery = response.Header.Get("X-Cage-Recovery")
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		e := agentHTTPError(response.StatusCode)
		e.Message += " " + agentRedact(strings.TrimSpace(string(body)))
		return result, e
	}
	if output != "" {
		if !filepath.IsAbs(output) {
			return result, &AgentError{Code: "invalid_input", Message: "output must be an absolute new file path.", NextAction: "capabilities"}
		}
		f, err := os.CreateTemp(filepath.Dir(output), ".cage-agent-response-*")
		if err != nil {
			return result, err
		}
		defer os.Remove(f.Name())
		if err := host.ProtectFile(f.Name()); err != nil {
			f.Close()
			return result, err
		}
		_, copyErr := io.Copy(f, response.Body)
		err = errors.Join(copyErr, f.Sync(), f.Close())
		if err != nil {
			return result, err
		}
		if err = os.Link(f.Name(), output); err != nil {
			return result, err
		}
		result.Path = output
		return result, nil
	}
	if response.StatusCode == 204 || method == "HEAD" {
		return result, nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (32<<20)+1))
	if err != nil || len(data) > 32<<20 {
		return result, &AgentError{Code: "invalid_response", Message: "Response exceeds 32 MiB or was interrupted; use --output for a file response.", NextAction: "status"}
	}
	if !json.Valid(data) {
		return result, &AgentError{Code: "output_required", Message: "This response is not JSON. Use --output with a new absolute file path for binary or streaming content.", NextAction: "capabilities"}
	}
	result.Data = data
	return result, nil
}

func AgentCallTarget(ctx context.Context, configPath string, target AgentTarget, operation string, input json.RawMessage) (json.RawMessage, error) {
	for _, op := range AgentOperations() {
		if op.Name == operation {
			if err := agentValidateInput(input, op.InputSchema); err != nil {
				return nil, err
			}
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			result, err := AgentRequest(ctx, configPath, target, op.method, op.path, strings.NewReader(string(input)), "application/json", "")
			return result.Data, err
		}
	}
	return nil, &AgentError{Code: "unknown_operation", Message: "Unknown operation. Read capabilities or use request for an existing UI endpoint.", NextAction: "capabilities"}
}
