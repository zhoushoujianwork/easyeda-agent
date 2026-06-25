package protocol

// Message type discriminators carried on the connector WebSocket. Every frame
// is a JSON object with a "type" field that selects one of the structs below.
//
// Handshake flow (EasyEDA-idiomatic, since the connector runs in a webview that
// cannot use fetch/browser WebSocket): the connector dials ws://127.0.0.1:PORT/eda
// for each port in range; the daemon answers with a Handshake identifying itself;
// the connector validates the service and replies with Register, then Context.
// Liveness is a connector-driven Ping that the daemon answers with Pong.
const (
	TypeHandshake = "handshake"
	TypeRegister  = "register"
	TypeContext   = "context"
	TypePing      = "ping"
	TypePong      = "pong"
	TypeRequest   = "request"
	TypeResponse  = "response"
)

// Handshake is sent by the daemon immediately after a connector connects, so the
// connector can confirm it reached an easyeda-agent daemon (not some other local
// service) before registering.
type Handshake struct {
	Type    string `json:"type"`
	Service string `json:"service"`
	Version string `json:"version"`
}

// Ping is a liveness probe; the receiver echoes its ID back in a Pong.
type Ping struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

// Pong answers a Ping, echoing its ID.
type Pong struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

// Typed peeks only the discriminator of an inbound connector frame so the
// daemon can decide which concrete struct to decode it into.
type Typed struct {
	Type string `json:"type"`
}

// Register is the connector's first frame after the WebSocket opens. It
// declares the EasyEDA window identity and what the connector can do.
type Register struct {
	Type             string   `json:"type"`
	WindowID         string   `json:"windowId"`
	ConnectorVersion string   `json:"connectorVersion"`
	EasyEDAVersion   string   `json:"easyedaVersion"`
	Capabilities     []string `json:"capabilities"`
}

// ContextMessage reports the connector's current project/document context. It
// is sent on connect and whenever the active document changes.
type ContextMessage struct {
	Type         string `json:"type"`
	WindowID     string `json:"windowId"`
	ProjectUUID  string `json:"projectUuid"`
	ProjectName  string `json:"projectName"`
	DocumentUUID string `json:"documentUuid"`
	DocumentType string `json:"documentType"`
	TabID        string `json:"tabId"`
	Unit         string `json:"unit"`
}

// Context returns the project/document context carried by this message.
func (m ContextMessage) Context() Context {
	return Context{
		ProjectUUID:  m.ProjectUUID,
		ProjectName:  m.ProjectName,
		DocumentUUID: m.DocumentUUID,
		DocumentType: m.DocumentType,
		TabID:        m.TabID,
		Unit:         m.Unit,
	}
}
