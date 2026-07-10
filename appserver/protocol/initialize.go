package protocol

const (
	ProtocolVersion = "gollem.appserver.v0"
	SchemaVersion   = "gollem.appserver.schema.v1"
)

// ImplementationInfo identifies one side of the app-server connection.
type ImplementationInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// InitializeParams is sent by the client as the first request on a connection.
type InitializeParams struct {
	ClientInfo   ImplementationInfo     `json:"clientInfo"`
	Capabilities InitializeCapabilities `json:"capabilities,omitempty"`
}

// InitializeCapabilities are client-advertised connection capabilities.
type InitializeCapabilities struct {
	OptOutNotificationMethods []string        `json:"optOutNotificationMethods,omitempty"`
	Experimental              map[string]bool `json:"experimental,omitempty"`
}

// InitializeResponse is returned after a successful initialize request.
type InitializeResponse struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      ImplementationInfo `json:"serverInfo"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	Methods         []MethodInfo       `json:"methods,omitempty"`
}

// ServerCapabilities advertises app-server features available on the current build.
type ServerCapabilities struct {
	ProtocolSchema bool     `json:"protocolSchema"`
	Unavailable    bool     `json:"unavailable"`
	Experimental   []string `json:"experimental,omitempty"`
}

// DefaultInitializeResponse returns conservative protocol capabilities for
// the current foundation package. Runtime packages can add methods as they
// become implemented.
func DefaultInitializeResponse(server ImplementationInfo) InitializeResponse {
	return InitializeResponse{
		ProtocolVersion: ProtocolVersion,
		ServerInfo:      server,
		Capabilities: ServerCapabilities{
			ProtocolSchema: true,
			Unavailable:    true,
		},
		Methods: Methods(),
	}
}
