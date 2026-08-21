package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ServerRequest is the exact standalone public server-to-client request union.
// It deliberately does not replace Gollem's live request envelopes or bindings.
type ServerRequest struct {
	Method string          `json:"method"`
	ID     RequestId       `json:"id"`
	Params json.RawMessage `json:"params"`
}

type serverRequestVariant struct {
	Method      string
	Title       string
	Description string
	ParamType   string
}

var serverRequestVariants = []serverRequestVariant{
	{
		Method: "item/commandExecution/requestApproval", Title: "Item/commandExecution/requestApprovalRequest",
		Description: "NEW APIs Sent when approval is requested for a specific command execution. This request is used for Turns started via turn/start.",
		ParamType:   "CommandExecutionRequestApprovalParams",
	},
	{
		Method: "item/fileChange/requestApproval", Title: "Item/fileChange/requestApprovalRequest",
		Description: "Sent when approval is requested for a specific file change. This request is used for Turns started via turn/start.",
		ParamType:   "FileChangeRequestApprovalParams",
	},
	{
		Method: "item/tool/requestUserInput", Title: "Item/tool/requestUserInputRequest",
		Description: "EXPERIMENTAL - Request input from the user for a tool call.",
		ParamType:   "ToolRequestUserInputParams",
	},
	{
		Method: "mcpServer/elicitation/request", Title: "McpServer/elicitation/requestRequest",
		Description: "Request input for an MCP server elicitation.",
		ParamType:   "McpServerElicitationRequestParams",
	},
	{
		Method: "item/permissions/requestApproval", Title: "Item/permissions/requestApprovalRequest",
		Description: "Request approval for additional permissions from the user.",
		ParamType:   "PermissionsRequestApprovalParams",
	},
	{
		Method: "item/tool/call", Title: "Item/tool/callRequest",
		Description: "Execute a dynamic tool call on the client.",
		ParamType:   "DynamicToolCallParams",
	},
	{
		Method: "account/chatgptAuthTokens/refresh", Title: "Account/chatgptAuthTokens/refreshRequest",
		ParamType: "ChatgptAuthTokensRefreshParams",
	},
	{
		Method: "attestation/generate", Title: "Attestation/generateRequest",
		Description: "Generate a fresh upstream attestation result on demand.",
		ParamType:   "AttestationGenerateParams",
	},
	{
		Method: "currentTime/read", Title: "CurrentTime/readRequest",
		Description: "Read the current time from an external clock owned by the client.",
		ParamType:   "CurrentTimeReadParams",
	},
	{
		Method: "applyPatchApproval", Title: "ApplyPatchApprovalRequest",
		Description: "DEPRECATED APIs below Request to approve a patch. This request is used for Turns started via the legacy APIs (i.e. SendUserTurn, SendUserMessage).",
		ParamType:   "ApplyPatchApprovalParams",
	},
	{
		Method: "execCommandApproval", Title: "ExecCommandApprovalRequest",
		Description: "Request to exec a command. This request is used for Turns started via the legacy APIs (i.e. SendUserTurn, SendUserMessage).",
		ParamType:   "ExecCommandApprovalParams",
	},
}

func (r ServerRequest) MarshalJSON() ([]byte, error) {
	params, err := canonicalServerRequestParams(r.Method, r.Params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Method string          `json:"method"`
		ID     RequestId       `json:"id"`
		Params json.RawMessage `json:"params"`
	}{Method: r.Method, ID: r.ID, Params: params})
}

func (r *ServerRequest) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errors.New("decode server request into nil receiver")
	}
	const objectName = "server request"
	payload, err := decodeRustSerdeObject(data, objectName, "method", "id", "params")
	if err != nil {
		return err
	}
	method, err := decodeRequiredThreadItemValue[string](payload, objectName, "method")
	if err != nil {
		return err
	}
	id, err := decodeRequiredThreadItemValue[RequestId](payload, objectName, "id")
	if err != nil {
		return err
	}
	paramsRaw, ok := payload["params"]
	if !ok || isJSONNull(paramsRaw) {
		return fmt.Errorf("%s requires params", objectName)
	}
	params, err := canonicalServerRequestParams(method, paramsRaw)
	if err != nil {
		return err
	}
	*r = ServerRequest{Method: method, ID: id, Params: params}
	return nil
}

func canonicalServerRequestParams(method string, raw json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "item/commandExecution/requestApproval":
		return decodeServerRequestParams[CommandExecutionRequestApprovalParams](method, raw)
	case "item/fileChange/requestApproval":
		return decodeServerRequestParams[FileChangeRequestApprovalParams](method, raw)
	case "item/tool/requestUserInput":
		return decodeServerRequestParams[ToolRequestUserInputParams](method, raw)
	case "mcpServer/elicitation/request":
		return decodeServerRequestParams[McpServerElicitationRequestParams](method, raw)
	case "item/permissions/requestApproval":
		return decodeServerRequestParams[PermissionsRequestApprovalParams](method, raw)
	case "item/tool/call":
		return decodeServerRequestParams[DynamicToolCallParams](method, raw)
	case "account/chatgptAuthTokens/refresh":
		return decodeServerRequestParams[ChatgptAuthTokensRefreshParams](method, raw)
	case "attestation/generate":
		return decodeServerRequestParams[AttestationGenerateParams](method, raw)
	case "currentTime/read":
		return decodeServerRequestParams[CurrentTimeReadParams](method, raw)
	case "applyPatchApproval":
		return decodeServerRequestParams[ApplyPatchApprovalParams](method, raw)
	case "execCommandApproval":
		return decodeServerRequestParams[ExecCommandApprovalParams](method, raw)
	default:
		return nil, fmt.Errorf("unknown server request method %q", method)
	}
}

func decodeServerRequestParams[T any](method string, raw json.RawMessage) (json.RawMessage, error) {
	var params T
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode server request params for %s: %w", method, err)
	}
	canonical, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode server request params for %s: %w", method, err)
	}
	return canonical, nil
}

var (
	_ json.Marshaler   = ServerRequest{}
	_ json.Unmarshaler = (*ServerRequest)(nil)
)
