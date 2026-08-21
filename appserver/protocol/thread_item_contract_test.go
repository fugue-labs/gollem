package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestThreadItemSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	threadItem, ok := defs["ThreadItem"].(Schema)
	if !ok {
		t.Fatal("$defs missing ThreadItem")
	}
	variants, ok := threadItem["oneOf"].([]any)
	if !ok || len(variants) != 18 {
		t.Fatalf("ThreadItem variants = %#v", threadItem["oneOf"])
	}
	want := []struct {
		itemType string
		required []string
	}{
		{itemType: "userMessage", required: []string{"type", "id", "clientId", "content"}},
		{itemType: "hookPrompt", required: []string{"type", "id", "fragments"}},
		{itemType: "agentMessage", required: []string{"type", "id", "text", "phase", "memoryCitation"}},
		{itemType: "plan", required: []string{"type", "id", "text"}},
		{itemType: "reasoning", required: []string{"type", "id", "summary", "content"}},
		{
			itemType: "commandExecution",
			required: []string{
				"type", "id", "command", "cwd", "processId", "source", "status", "commandActions",
				"aggregatedOutput", "exitCode", "durationMs",
			},
		},
		{itemType: "fileChange", required: []string{"type", "id", "changes", "status"}},
		{
			itemType: "mcpToolCall",
			required: []string{
				"type", "id", "server", "tool", "status", "arguments", "appContext", "pluginId",
				"result", "error", "durationMs",
			},
		},
		{
			itemType: "dynamicToolCall",
			required: []string{"type", "id", "namespace", "tool", "arguments", "status", "contentItems", "success", "durationMs"},
		},
		{
			itemType: "collabAgentToolCall",
			required: []string{
				"type", "id", "tool", "status", "senderThreadId", "receiverThreadIds", "prompt",
				"model", "reasoningEffort", "agentsStates",
			},
		},
		{itemType: "subAgentActivity", required: []string{"type", "id", "kind", "agentThreadId", "agentPath"}},
		{itemType: "webSearch", required: []string{"type", "id", "query", "action"}},
		{itemType: "imageView", required: []string{"type", "id", "path"}},
		{itemType: "sleep", required: []string{"type", "id", "durationMs"}},
		{itemType: "imageGeneration", required: []string{"type", "id", "status", "revisedPrompt", "result"}},
		{itemType: "enteredReviewMode", required: []string{"type", "id", "review"}},
		{itemType: "exitedReviewMode", required: []string{"type", "id", "review"}},
		{itemType: "contextCompaction", required: []string{"type", "id"}},
	}
	byType := make(map[string]Schema, len(variants))
	for index, expected := range want {
		variant := variants[index].(Schema)
		if variant["additionalProperties"] != false {
			t.Fatalf("ThreadItem %s allows extra fields", expected.itemType)
		}
		properties := variant["properties"].(Schema)
		if !reflect.DeepEqual(properties["type"].(Schema)["enum"], []any{expected.itemType}) {
			t.Fatalf("ThreadItem variant %d type = %#v", index, properties["type"])
		}
		if !slices.Equal(schemaRequiredNames(variant), expected.required) {
			t.Fatalf("ThreadItem %s required = %v, want %v", expected.itemType, schemaRequiredNames(variant), expected.required)
		}
		byType[expected.itemType] = variant
	}

	assertThreadItemArrayRef(t, byType["userMessage"], "content", "UserInput")
	assertThreadItemArrayRef(t, byType["hookPrompt"], "fragments", "HookPromptFragment")
	assertThreadItemNullableRef(t, byType["agentMessage"], "phase", "MessagePhase")
	assertThreadItemNullableRef(t, byType["agentMessage"], "memoryCitation", "MemoryCitation")
	command := threadItemProperties(byType["commandExecution"])
	assertThreadItemRef(t, command["cwd"], "LegacyAppPathString")
	assertThreadItemRef(t, command["source"], "CommandExecutionSource")
	assertThreadItemRef(t, command["status"], "CommandExecutionStatus")
	assertThreadItemArrayRef(t, byType["commandExecution"], "commandActions", "CommandAction")
	assertThreadItemArrayRef(t, byType["fileChange"], "changes", "FileUpdateChange")
	assertThreadItemRef(t, threadItemProperties(byType["fileChange"])["status"], "PatchApplyStatus")
	mcp := threadItemProperties(byType["mcpToolCall"])
	assertThreadItemRef(t, mcp["arguments"], "JsonValue")
	assertThreadItemNullableRef(t, byType["mcpToolCall"], "appContext", "McpToolCallAppContext")
	assertThreadItemNullableRef(t, byType["mcpToolCall"], "result", "McpToolCallResult")
	assertThreadItemNullableRef(t, byType["mcpToolCall"], "error", "McpToolCallError")
	if slices.Contains(schemaRequiredNames(byType["mcpToolCall"]), "mcpAppResourceUri") {
		t.Fatal("mcpAppResourceUri is required")
	}
	if !reflect.DeepEqual(mcp["mcpAppResourceUri"], Schema{"type": "string"}) {
		t.Fatalf("mcpAppResourceUri = %#v", mcp["mcpAppResourceUri"])
	}
	dynamic := threadItemProperties(byType["dynamicToolCall"])
	assertThreadItemRef(t, dynamic["arguments"], "JsonValue")
	assertThreadItemNullableArrayRef(t, dynamic["contentItems"], "DynamicToolCallOutputContentItem")
	agentStates := threadItemProperties(byType["collabAgentToolCall"])["agentsStates"].(Schema)
	if !reflect.DeepEqual(agentStates["additionalProperties"], Schema{"$ref": "#/$defs/CollabAgentState"}) ||
		agentStates["x-gollem-typescript-optional-map"] != true {
		t.Fatalf("agentsStates = %#v", agentStates)
	}
	sleepDuration := threadItemProperties(byType["sleep"])["durationMs"].(Schema)
	if sleepDuration["type"] != "integer" || sleepDuration["minimum"] != 0 {
		t.Fatalf("sleep durationMs = %#v", sleepDuration)
	}
	image := byType["imageGeneration"]
	if slices.Contains(schemaRequiredNames(image), "savedPath") {
		t.Fatal("imageGeneration savedPath is required")
	}
	assertThreadItemRef(t, threadItemProperties(image)["savedPath"], "AbsolutePathBuf")
}

func TestThreadItemWireValidationAndCanonicalization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: `{ "content": [{"type":"text","text":"hello","text_elements":[]}], "clientId": null, "id": "user-1", "type": "userMessage" }`,
			want:  `{"type":"userMessage","id":"user-1","clientId":null,"content":[{"type":"text","text":"hello","text_elements":[]}]}`,
		},
		{
			input: `{"type":"hookPrompt","id":"hook-1","fragments":[{"hookRunId":"run-1","text":"prompt"}]}`,
			want:  `{"type":"hookPrompt","id":"hook-1","fragments":[{"text":"prompt","hookRunId":"run-1"}]}`,
		},
		{
			input: `{"type":"agentMessage","id":"agent-1","text":"done","phase":"final_answer","memoryCitation":{"threadIds":["thread-2"],"entries":[{"note":"source","lineEnd":2,"lineStart":1,"path":"MEMORY.md"}]}}`,
			want:  `{"type":"agentMessage","id":"agent-1","text":"done","phase":"final_answer","memoryCitation":{"entries":[{"path":"MEMORY.md","lineStart":1,"lineEnd":2,"note":"source"}],"threadIds":["thread-2"]}}`,
		},
		{
			input: `{"type":"agentMessage","id":"agent-2","text":"working","phase":null,"memoryCitation":null}`,
			want:  `{"type":"agentMessage","id":"agent-2","text":"working","phase":null,"memoryCitation":null}`,
		},
		{input: `{"type":"plan","id":"plan-1","text":"ship"}`, want: `{"type":"plan","id":"plan-1","text":"ship"}`},
		{
			input: `{"type":"reasoning","id":"reason-1","summary":["first"],"content":["detail"]}`,
			want:  `{"type":"reasoning","id":"reason-1","summary":["first"],"content":["detail"]}`,
		},
		{
			input: `{"type":"commandExecution","id":"command-1","command":"cat README.md","cwd":"/workspace","processId":null,"source":"unifiedExecInteraction","status":"completed","commandActions":[{"type":"read","command":"cat README.md","name":"README.md","path":"/workspace/README.md"}],"aggregatedOutput":"ok","exitCode":0,"durationMs":12}`,
			want:  `{"type":"commandExecution","id":"command-1","command":"cat README.md","cwd":"/workspace","processId":null,"source":"unifiedExecInteraction","status":"completed","commandActions":[{"type":"read","command":"cat README.md","name":"README.md","path":"/workspace/README.md"}],"aggregatedOutput":"ok","exitCode":0,"durationMs":12}`,
		},
		{
			input: `{"type":"fileChange","id":"file-1","changes":[{"diff":"+new","kind":{"move_path":null,"type":"update"},"path":"file.txt"}],"status":"failed"}`,
			want:  `{"type":"fileChange","id":"file-1","changes":[{"path":"file.txt","kind":{"type":"update","move_path":null},"diff":"+new"}],"status":"failed"}`,
		},
		{
			input: `{"type":"mcpToolCall","id":"mcp-1","server":"docs","tool":"search","status":"completed","arguments":{"z":1,"large":18446744073709551616},"appContext":null,"pluginId":null,"result":{"content":[null,{"z":2,"a":1}],"structuredContent":null,"_meta":null},"error":null,"durationMs":null}`,
			want:  `{"type":"mcpToolCall","id":"mcp-1","server":"docs","tool":"search","status":"completed","arguments":{"large":18446744073709551616,"z":1},"appContext":null,"pluginId":null,"result":{"content":[null,{"a":1,"z":2}],"structuredContent":null,"_meta":null},"error":null,"durationMs":null}`,
		},
		{
			input: `{"type":"mcpToolCall","id":"mcp-2","server":"docs","tool":"open","status":"failed","arguments":null,"appContext":{"connectorId":"connector","linkId":null,"resourceUri":"resource://item","appName":null,"templateId":null,"actionName":null},"mcpAppResourceUri":"resource://legacy","pluginId":"plugin","result":null,"error":{"message":"failed"},"durationMs":5}`,
			want:  `{"type":"mcpToolCall","id":"mcp-2","server":"docs","tool":"open","status":"failed","arguments":null,"appContext":{"connectorId":"connector","linkId":null,"resourceUri":"resource://item","appName":null,"templateId":null,"actionName":null},"mcpAppResourceUri":"resource://legacy","pluginId":"plugin","result":null,"error":{"message":"failed"},"durationMs":5}`,
		},
		{
			input: `{"type":"dynamicToolCall","id":"dynamic-1","namespace":null,"tool":"render","arguments":[18446744073709551616],"status":"inProgress","contentItems":[{"type":"inputText","text":"hello"}],"success":null,"durationMs":null}`,
			want:  `{"type":"dynamicToolCall","id":"dynamic-1","namespace":null,"tool":"render","arguments":[18446744073709551616],"status":"inProgress","contentItems":[{"type":"inputText","text":"hello"}],"success":null,"durationMs":null}`,
		},
		{
			input: `{"type":"collabAgentToolCall","id":"collab-1","tool":"spawnAgent","status":"inProgress","senderThreadId":"thread-1","receiverThreadIds":["thread-2"],"prompt":null,"model":"gpt-5","reasoningEffort":"high","agentsStates":{"thread-z":{"status":"running","message":null},"thread-a":{"status":"completed","message":"done"}}}`,
			want:  `{"type":"collabAgentToolCall","id":"collab-1","tool":"spawnAgent","status":"inProgress","senderThreadId":"thread-1","receiverThreadIds":["thread-2"],"prompt":null,"model":"gpt-5","reasoningEffort":"high","agentsStates":{"thread-a":{"status":"completed","message":"done"},"thread-z":{"status":"running","message":null}}}`,
		},
		{
			input: `{"type":"subAgentActivity","id":"activity-1","kind":"interacted","agentThreadId":"thread-2","agentPath":"agent/path"}`,
			want:  `{"type":"subAgentActivity","id":"activity-1","kind":"interacted","agentThreadId":"thread-2","agentPath":"agent/path"}`,
		},
		{
			input: `{"type":"webSearch","id":"web-1","query":"gollem","action":{"queries":null,"query":"gollem","type":"search"}}`,
			want:  `{"type":"webSearch","id":"web-1","query":"gollem","action":{"type":"search","query":"gollem","queries":null}}`,
		},
		{input: `{"type":"imageView","id":"view-1","path":"relative.png"}`, want: `{"type":"imageView","id":"view-1","path":"relative.png"}`},
		{input: `{"type":"sleep","id":"sleep-1","durationMs":18446744073709551615}`, want: `{"type":"sleep","id":"sleep-1","durationMs":18446744073709551615}`},
		{
			input: `{"type":"imageGeneration","id":"image-1","status":"completed","revisedPrompt":null,"result":"base64","savedPath":"/workspace/../workspace/image.png"}`,
			want:  `{"type":"imageGeneration","id":"image-1","status":"completed","revisedPrompt":null,"result":"base64","savedPath":"/workspace/image.png"}`,
		},
		{input: `{"type":"enteredReviewMode","id":"review-1","review":"review"}`, want: `{"type":"enteredReviewMode","id":"review-1","review":"review"}`},
		{input: `{"type":"exitedReviewMode","id":"review-2","review":"done"}`, want: `{"type":"exitedReviewMode","id":"review-2","review":"done"}`},
		{input: `{"type":"contextCompaction","id":"compact-1"}`, want: `{"type":"contextCompaction","id":"compact-1"}`},
	}
	for _, testCase := range tests {
		var item ThreadItem
		if err := json.Unmarshal([]byte(testCase.input), &item); err != nil {
			t.Errorf("Unmarshal(%s): %v", testCase.input, err)
			continue
		}
		encoded, err := json.Marshal(item)
		if err != nil || string(encoded) != testCase.want {
			t.Errorf("round trip %s = %s, %v; want %s", testCase.input, encoded, err, testCase.want)
		}
	}
}

func TestThreadItemWireRejectsMalformedVariants(t *testing.T) {
	invalid := []string{
		`null`, `[]`, `{}`, `{"type":null}`, `{"type":"unknown","id":"item"}`,
		`{"type":"userMessage","id":"u","content":[]}`,
		`{"type":"userMessage","id":"u","clientId":null,"content":null}`,
		`{"type":"userMessage","id":"u","clientId":null,"content":[null]}`,
		`{"type":"userMessage","id":null,"clientId":null,"content":[]}`,
		`{"type":"userMessage","id":"u","clientId":null,"content":[],"review":"crossed"}`,
		`{"type":"hookPrompt","id":"h"}`,
		`{"type":"hookPrompt","id":null,"fragments":[]}`,
		`{"type":"hookPrompt","id":"h","fragments":[null]}`,
		`{"type":"hookPrompt","id":"h","fragments":[{"text":"x"}]}`,
		`{"type":"hookPrompt","id":"h","fragments":[],"query":"crossed"}`,
		`{"type":"agentMessage","id":"a","text":"x","phase":null}`,
		`{"type":"agentMessage","id":null,"text":"x","phase":null,"memoryCitation":null}`,
		`{"type":"agentMessage","id":"a","text":null,"phase":null,"memoryCitation":null}`,
		`{"type":"agentMessage","id":"a","text":"x","phase":"unknown","memoryCitation":null}`,
		`{"type":"agentMessage","id":"a","text":"x","phase":null,"memoryCitation":{}}`,
		`{"type":"agentMessage","id":"a","text":"x","phase":null,"memoryCitation":{"entries":[],"threadIds":[null]}}`,
		`{"type":"agentMessage","id":"a","text":"x","phase":null,"memoryCitation":{"entries":[],"threadIds":[],"extra":true}}`,
		`{"type":"agentMessage","id":"a","text":"x","phase":null,"memoryCitation":null,"review":"crossed"}`,
		`{"type":"plan","id":null,"text":"x"}`,
		`{"type":"plan","id":"p","text":"x","review":"crossed"}`,
		`{"type":"reasoning","id":"r","summary":null,"content":[]}`,
		`{"type":"reasoning","id":null,"summary":[],"content":[]}`,
		`{"type":"reasoning","id":"r","summary":[null],"content":[]}`,
		`{"type":"reasoning","id":"r","summary":[],"content":[1]}`,
		`{"type":"reasoning","id":"r","summary":[],"content":[],"query":"crossed"}`,
		`{"type":"commandExecution","id":"c","command":"pwd","cwd":"/w","processId":null,"source":"agent","status":"completed","commandActions":[],"aggregatedOutput":null,"exitCode":null}`,
		`{"type":"commandExecution","id":null,"command":"pwd","cwd":"/w","processId":null,"source":"agent","status":"completed","commandActions":[],"aggregatedOutput":null,"exitCode":null,"durationMs":null}`,
		`{"type":"commandExecution","id":"c","command":null,"cwd":"/w","processId":null,"source":"agent","status":"completed","commandActions":[],"aggregatedOutput":null,"exitCode":null,"durationMs":null}`,
		`{"type":"commandExecution","id":"c","command":"pwd","cwd":null,"processId":null,"source":"agent","status":"completed","commandActions":[],"aggregatedOutput":null,"exitCode":null,"durationMs":null}`,
		`{"type":"commandExecution","id":"c","command":"pwd","cwd":"/w","processId":1,"source":"agent","status":"completed","commandActions":[],"aggregatedOutput":null,"exitCode":null,"durationMs":null}`,
		`{"type":"commandExecution","id":"c","command":"pwd","cwd":"/w","processId":null,"source":"other","status":"completed","commandActions":[],"aggregatedOutput":null,"exitCode":null,"durationMs":null}`,
		`{"type":"commandExecution","id":"c","command":"pwd","cwd":"/w","processId":null,"source":"agent","status":"other","commandActions":[],"aggregatedOutput":null,"exitCode":null,"durationMs":null}`,
		`{"type":"commandExecution","id":"c","command":"pwd","cwd":"/w","processId":null,"source":"agent","status":"completed","commandActions":[null],"aggregatedOutput":null,"exitCode":null,"durationMs":null}`,
		`{"type":"commandExecution","id":"c","command":"pwd","cwd":"/w","processId":null,"source":"agent","status":"completed","commandActions":[],"aggregatedOutput":1,"exitCode":null,"durationMs":null}`,
		`{"type":"commandExecution","id":"c","command":"pwd","cwd":"/w","processId":null,"source":"agent","status":"completed","commandActions":[],"aggregatedOutput":null,"exitCode":2147483648,"durationMs":null}`,
		`{"type":"commandExecution","id":"c","command":"pwd","cwd":"/w","processId":null,"source":"agent","status":"completed","commandActions":[],"aggregatedOutput":null,"exitCode":1.5,"durationMs":null}`,
		`{"type":"commandExecution","id":"c","command":"pwd","cwd":"/w","processId":null,"source":"agent","status":"completed","commandActions":[],"aggregatedOutput":null,"exitCode":null,"durationMs":null,"query":"crossed"}`,
		`{"type":"fileChange","id":"f","changes":null,"status":"completed"}`,
		`{"type":"fileChange","id":null,"changes":[],"status":"completed"}`,
		`{"type":"fileChange","id":"f","changes":[null],"status":"completed"}`,
		`{"type":"fileChange","id":"f","changes":[{"path":null,"kind":{"type":"add"},"diff":""}],"status":"completed"}`,
		`{"type":"fileChange","id":"f","changes":[{"path":"x","diff":""}],"status":"completed"}`,
		`{"type":"fileChange","id":"f","changes":[{"path":"x","kind":{"move_path":null},"diff":""}],"status":"completed"}`,
		`{"type":"fileChange","id":"f","changes":[{"path":"x","kind":{"type":"other"},"diff":""}],"status":"completed"}`,
		`{"type":"fileChange","id":"f","changes":[{"path":"x","kind":{"type":"add"}}],"status":"completed"}`,
		`{"type":"fileChange","id":"f","changes":[{"path":"x","kind":{"type":"add"},"diff":"","extra":true}],"status":"completed"}`,
		`{"type":"fileChange","id":"f","changes":[{"path":"x","kind":{"type":"update","movePath":null},"diff":""}],"status":"completed"}`,
		`{"type":"fileChange","id":"f","changes":[{"path":"x","kind":{"type":"update"},"diff":""}],"status":"completed"}`,
		`{"type":"fileChange","id":"f","changes":[{"path":"x","kind":{"type":"add","move_path":null},"diff":""}],"status":"completed"}`,
		`{"type":"fileChange","id":"f","changes":[],"status":"other"}`,
		`{"type":"fileChange","id":"f","changes":[],"status":"completed","query":"crossed"}`,
		`{"type":"mcpToolCall","id":"m","server":"s","tool":"t","status":"completed","arguments":null,"appContext":null,"pluginId":null,"result":null,"error":null}`,
		`{"type":"mcpToolCall","id":null,"server":"s","tool":"t","status":"completed","arguments":null,"appContext":null,"pluginId":null,"result":null,"error":null,"durationMs":null}`,
		`{"type":"mcpToolCall","id":"m","server":null,"tool":"t","status":"completed","arguments":null,"appContext":null,"pluginId":null,"result":null,"error":null,"durationMs":null}`,
		`{"type":"mcpToolCall","id":"m","server":"s","tool":null,"status":"completed","arguments":null,"appContext":null,"pluginId":null,"result":null,"error":null,"durationMs":null}`,
		`{"type":"mcpToolCall","id":"m","server":"s","tool":"t","status":"other","arguments":null,"appContext":null,"pluginId":null,"result":null,"error":null,"durationMs":null}`,
		`{"type":"mcpToolCall","id":"m","server":"s","tool":"t","status":"completed","appContext":null,"pluginId":null,"result":null,"error":null,"durationMs":null}`,
		`{"type":"mcpToolCall","id":"m","server":"s","tool":"t","status":"completed","arguments":null,"appContext":{},"pluginId":null,"result":null,"error":null,"durationMs":null}`,
		`{"type":"mcpToolCall","id":"m","server":"s","tool":"t","status":"completed","arguments":null,"appContext":null,"mcpAppResourceUri":null,"pluginId":null,"result":null,"error":null,"durationMs":null}`,
		`{"type":"mcpToolCall","id":"m","server":"s","tool":"t","status":"completed","arguments":null,"appContext":null,"pluginId":1,"result":null,"error":null,"durationMs":null}`,
		`{"type":"mcpToolCall","id":"m","server":"s","tool":"t","status":"completed","arguments":null,"appContext":null,"pluginId":null,"result":{},"error":null,"durationMs":null}`,
		`{"type":"mcpToolCall","id":"m","server":"s","tool":"t","status":"completed","arguments":null,"appContext":null,"pluginId":null,"result":null,"durationMs":null}`,
		`{"type":"mcpToolCall","id":"m","server":"s","tool":"t","status":"completed","arguments":null,"appContext":null,"pluginId":null,"result":null,"error":{},"durationMs":null}`,
		`{"type":"mcpToolCall","id":"m","server":"s","tool":"t","status":"completed","arguments":null,"appContext":null,"pluginId":null,"result":null,"error":{"message":"x","extra":true},"durationMs":null}`,
		`{"type":"mcpToolCall","id":"m","server":"s","tool":"t","status":"completed","arguments":null,"appContext":null,"pluginId":null,"result":null,"error":null,"durationMs":null,"query":"crossed"}`,
		`{"type":"dynamicToolCall","id":"d","namespace":null,"tool":"t","arguments":null,"status":"completed","contentItems":null,"success":null}`,
		`{"type":"dynamicToolCall","id":null,"namespace":null,"tool":"t","arguments":null,"status":"completed","contentItems":null,"success":null,"durationMs":null}`,
		`{"type":"dynamicToolCall","id":"d","namespace":1,"tool":"t","arguments":null,"status":"completed","contentItems":null,"success":null,"durationMs":null}`,
		`{"type":"dynamicToolCall","id":"d","namespace":null,"tool":null,"arguments":null,"status":"completed","contentItems":null,"success":null,"durationMs":null}`,
		`{"type":"dynamicToolCall","id":"d","namespace":null,"tool":"t","status":"completed","contentItems":null,"success":null,"durationMs":null}`,
		`{"type":"dynamicToolCall","id":"d","namespace":null,"tool":"t","arguments":null,"status":"other","contentItems":null,"success":null,"durationMs":null}`,
		`{"type":"dynamicToolCall","id":"d","namespace":null,"tool":"t","arguments":null,"status":"completed","contentItems":[null],"success":null,"durationMs":null}`,
		`{"type":"dynamicToolCall","id":"d","namespace":null,"tool":"t","arguments":null,"status":"completed","contentItems":{},"success":null,"durationMs":null}`,
		`{"type":"dynamicToolCall","id":"d","namespace":null,"tool":"t","arguments":null,"status":"completed","contentItems":null,"success":"yes","durationMs":null}`,
		`{"type":"dynamicToolCall","id":"d","namespace":null,"tool":"t","arguments":null,"status":"completed","contentItems":null,"success":null,"durationMs":null,"query":"crossed"}`,
		`{"type":"collabAgentToolCall","id":"c","tool":"spawnAgent","status":"completed","senderThreadId":"s","receiverThreadIds":[],"prompt":null,"model":null,"reasoningEffort":null}`,
		`{"type":"collabAgentToolCall","id":null,"tool":"spawnAgent","status":"completed","senderThreadId":"s","receiverThreadIds":[],"prompt":null,"model":null,"reasoningEffort":null,"agentsStates":{}}`,
		`{"type":"collabAgentToolCall","id":"c","tool":"other","status":"completed","senderThreadId":"s","receiverThreadIds":[],"prompt":null,"model":null,"reasoningEffort":null,"agentsStates":{}}`,
		`{"type":"collabAgentToolCall","id":"c","tool":"spawnAgent","status":"other","senderThreadId":"s","receiverThreadIds":[],"prompt":null,"model":null,"reasoningEffort":null,"agentsStates":{}}`,
		`{"type":"collabAgentToolCall","id":"c","tool":"spawnAgent","status":"completed","senderThreadId":null,"receiverThreadIds":[],"prompt":null,"model":null,"reasoningEffort":null,"agentsStates":{}}`,
		`{"type":"collabAgentToolCall","id":"c","tool":"spawnAgent","status":"completed","senderThreadId":"s","receiverThreadIds":[null],"prompt":null,"model":null,"reasoningEffort":null,"agentsStates":{}}`,
		`{"type":"collabAgentToolCall","id":"c","tool":"spawnAgent","status":"completed","senderThreadId":"s","receiverThreadIds":[],"prompt":1,"model":null,"reasoningEffort":null,"agentsStates":{}}`,
		`{"type":"collabAgentToolCall","id":"c","tool":"spawnAgent","status":"completed","senderThreadId":"s","receiverThreadIds":[],"prompt":null,"model":1,"reasoningEffort":null,"agentsStates":{}}`,
		`{"type":"collabAgentToolCall","id":"c","tool":"spawnAgent","status":"completed","senderThreadId":"s","receiverThreadIds":[],"prompt":null,"model":null,"reasoningEffort":"","agentsStates":{}}`,
		`{"type":"collabAgentToolCall","id":"c","tool":"spawnAgent","status":"completed","senderThreadId":"s","receiverThreadIds":[],"prompt":null,"model":null,"reasoningEffort":null,"agentsStates":null}`,
		`{"type":"collabAgentToolCall","id":"c","tool":"spawnAgent","status":"completed","senderThreadId":"s","receiverThreadIds":[],"prompt":null,"model":null,"reasoningEffort":null,"agentsStates":{"a":null}}`,
		`{"type":"collabAgentToolCall","id":"c","tool":"spawnAgent","status":"completed","senderThreadId":"s","receiverThreadIds":[],"prompt":null,"model":null,"reasoningEffort":null,"agentsStates":{"a":{}}}`,
		`{"type":"collabAgentToolCall","id":"c","tool":"spawnAgent","status":"completed","senderThreadId":"s","receiverThreadIds":[],"prompt":null,"model":null,"reasoningEffort":null,"agentsStates":{},"query":"crossed"}`,
		`{"type":"subAgentActivity","id":"s","kind":"other","agentThreadId":"t","agentPath":"a"}`,
		`{"type":"subAgentActivity","id":null,"kind":"started","agentThreadId":"t","agentPath":"a"}`,
		`{"type":"subAgentActivity","id":"s","kind":"started","agentThreadId":null,"agentPath":"a"}`,
		`{"type":"subAgentActivity","id":"s","kind":"started","agentThreadId":"t","agentPath":null}`,
		`{"type":"subAgentActivity","id":"s","kind":"started","agentThreadId":"t","agentPath":"a","query":"crossed"}`,
		`{"type":"webSearch","id":"w","query":"q"}`,
		`{"type":"webSearch","id":null,"query":"q","action":null}`,
		`{"type":"webSearch","id":"w","query":null,"action":null}`,
		`{"type":"webSearch","id":"w","query":"q","action":{"type":"open_page","url":null}}`,
		`{"type":"webSearch","id":"w","query":"q","action":null,"review":"crossed"}`,
		`{"type":"imageView","id":"i","path":null}`,
		`{"type":"imageView","id":null,"path":"x"}`,
		`{"type":"imageView","id":"i","path":"x","query":"crossed"}`,
		`{"type":"sleep","id":"s","durationMs":-1}`,
		`{"type":"sleep","id":null,"durationMs":1}`,
		`{"type":"sleep","id":"s","durationMs":1.5}`,
		`{"type":"sleep","id":"s","durationMs":18446744073709551616}`,
		`{"type":"sleep","id":"s","durationMs":1,"query":"crossed"}`,
		`{"type":"imageGeneration","id":"i","status":"completed","result":"x"}`,
		`{"type":"imageGeneration","id":null,"status":"completed","revisedPrompt":null,"result":"x"}`,
		`{"type":"imageGeneration","id":"i","status":null,"revisedPrompt":null,"result":"x"}`,
		`{"type":"imageGeneration","id":"i","status":"completed","revisedPrompt":null,"result":null}`,
		`{"type":"imageGeneration","id":"i","status":"completed","revisedPrompt":null,"result":"x","savedPath":null}`,
		`{"type":"imageGeneration","id":"i","status":"completed","revisedPrompt":null,"result":"x","query":"crossed"}`,
		`{"type":"enteredReviewMode","id":"r","review":"x","text":"crossed"}`,
		`{"type":"exitedReviewMode","id":"r","review":null}`,
		`{"type":"contextCompaction","id":"c","summary":"crossed"}`,
		`{"type":"contextCompaction","id":null}`,
	}
	assertRawJSONRejects[ThreadItem](t, invalid)
}

func TestThreadItemHelpersFailClosed(t *testing.T) {
	assertEmptyRawJSONMarshalRejects(t, ThreadItem{})
	var item *ThreadItem
	if err := item.UnmarshalJSON([]byte(`{"type":"contextCompaction","id":"item-1"}`)); err == nil {
		t.Fatal("nil ThreadItem receiver succeeded")
	}
	if _, err := json.Marshal(strictThreadItemPatchChangeKind{Type: "other"}); err == nil {
		t.Fatal("unknown strict patch kind marshaled")
	}
	for _, kind := range []string{"add", "delete"} {
		encoded, err := json.Marshal(strictThreadItemPatchChangeKind{Type: kind})
		if err != nil || string(encoded) != `{"type":"`+kind+`"}` {
			t.Fatalf("marshal strict %s kind = %s, %v", kind, encoded, err)
		}
	}
	if _, err := decodeRequiredThreadItemJSONValue(map[string]json.RawMessage{}, "test item", "arguments"); err == nil {
		t.Fatal("missing required JSON value succeeded")
	}
	if _, err := decodeRequiredClosedThreadItemString(
		map[string]json.RawMessage{}, "test item", "status", CommandExecutionStatusCompleted,
	); err == nil {
		t.Fatal("missing closed status succeeded")
	}
	if _, err := decodeRequiredThreadItemJSONValue(
		map[string]json.RawMessage{"arguments": json.RawMessage(`invalid`)}, "test item", "arguments",
	); err == nil {
		t.Fatal("malformed required JSON value succeeded")
	}
	if _, err := decodeRequiredNullableThreadItemArray[string](map[string]json.RawMessage{}, "test item", "items"); err == nil {
		t.Fatal("missing required nullable array succeeded")
	}
	if got, err := decodeRequiredNullableThreadItemArray[string](
		map[string]json.RawMessage{"items": json.RawMessage(`null`)}, "test item", "items",
	); err != nil || got != nil {
		t.Fatalf("nullable array = %#v, %v", got, err)
	}
	if _, err := decodeRequiredThreadItemFileChanges(
		map[string]json.RawMessage{"changes": json.RawMessage(`{}`)}, "test item", "changes",
	); err == nil {
		t.Fatal("object file-change array succeeded")
	}
	if _, err := decodeRequiredThreadItemAgentStates(
		map[string]json.RawMessage{"agentsStates": json.RawMessage(`[]`)}, "test item", "agentsStates",
	); err == nil {
		t.Fatal("array agentsStates succeeded")
	}
}

func TestThreadItemTypeScriptShapeAndBindingsRemainStandalone(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		"export type ThreadItem = {",
		`"agentsStates": { [key in string]?: CollabAgentState };`,
		"export type JsonValue = number | string | boolean | Array<JsonValue> | { [key: string]: JsonValue } | null;",
		`"metadata"?: Record<string, unknown> | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
	if len(JSONSchema()["$defs"].(Schema)) != 675 {
		t.Fatalf("definition count = %d, want 675", len(JSONSchema()["$defs"].(Schema)))
	}
	if len(WireTypeBindings()) != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", len(WireTypeBindings()), len(ItemPayloadBindings()))
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ThreadItem") || slices.Contains(binding.Result, "ThreadItem") {
			t.Fatalf("ThreadItem unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "ThreadItem" {
			t.Fatalf("ThreadItem unexpectedly bound to durable item %s", binding.Kind)
		}
	}
}

func threadItemProperties(schema Schema) Schema {
	return schema["properties"].(Schema)
}

func assertThreadItemRef(t *testing.T, raw any, name string) {
	t.Helper()
	want := Schema{"$ref": "#/$defs/" + name}
	if !reflect.DeepEqual(raw, want) {
		t.Fatalf("ThreadItem ref = %#v, want %#v", raw, want)
	}
}

func assertThreadItemArrayRef(t *testing.T, variant Schema, fieldName, name string) {
	t.Helper()
	want := Schema{"type": "array", "items": Schema{"$ref": "#/$defs/" + name}}
	if raw := threadItemProperties(variant)[fieldName]; !reflect.DeepEqual(raw, want) {
		t.Fatalf("ThreadItem %s = %#v, want %#v", fieldName, raw, want)
	}
}

func assertThreadItemNullableRef(t *testing.T, variant Schema, fieldName, name string) {
	t.Helper()
	assertNullableSchemaRef(t, threadItemProperties(variant)[fieldName], "#/$defs/"+name)
}

func assertThreadItemNullableArrayRef(t *testing.T, raw any, name string) {
	t.Helper()
	want := Schema{"anyOf": []any{
		Schema{"type": "array", "items": Schema{"$ref": "#/$defs/" + name}},
		Schema{"type": "null"},
	}}
	if !reflect.DeepEqual(raw, want) {
		t.Fatalf("ThreadItem nullable array = %#v, want %#v", raw, want)
	}
}
