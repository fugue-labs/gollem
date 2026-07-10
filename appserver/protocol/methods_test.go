package protocol

import "testing"

func TestMethodRegistryCountsAndKeyMethods(t *testing.T) {
	methods := Methods()
	if len(methods) != 224 {
		t.Fatalf("Methods() returned %d entries, want 224", len(methods))
	}

	counts := map[Surface]int{}
	for _, info := range methods {
		counts[info.Surface]++
	}
	wantCounts := map[Surface]int{
		SurfaceClientRequest:      125,
		SurfaceServerNotification: 70,
		SurfaceServerRequest:      11,
		SurfaceClientNotification: 1,
		SurfaceGollemExtension:    17,
	}
	for surface, want := range wantCounts {
		if got := counts[surface]; got != want {
			t.Fatalf("%s count = %d, want %d", surface, got, want)
		}
	}

	assertMethod(t, "initialize", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/start", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/resume", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/search", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/loaded/list", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/unsubscribe", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/compact/start", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/shellCommand", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/approveGuardianDeniedAction", SurfaceClientRequest, MethodDeferredStub)
	assertMethod(t, "thread/inject_items", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/rollback", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "memory/reset", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/settings/update", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/goal/get", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/goal/set", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/goal/clear", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/metadata/update", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/memoryMode/set", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/name/set", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/backgroundTerminals/list", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/backgroundTerminals/terminate", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "thread/backgroundTerminals/clean", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "turn/start", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "turn/interrupt", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "turn/steer", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "model/list", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "modelProvider/capabilities/read", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "collaborationMode/list", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "config/read", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "config/value/write", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "config/batchWrite", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "configRequirements/read", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "config/mcpServer/reload", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "mcpServerStatus/list", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "mcpServer/resource/read", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "mcpServer/tool/call", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "skills/list", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "plugin/list", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "plugin/installed", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "plugin/read", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "plugin/skill/read", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "environment/add", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "environment/info", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "experimentalFeature/list", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "experimentalFeature/enablement/set", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "permissionProfile/list", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "fs/readFile", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "git/status", SurfaceGollemExtension, MethodImplemented)
	assertMethod(t, "turn/retry", SurfaceGollemExtension, MethodImplemented)
	assertMethod(t, "provider/list", SurfaceGollemExtension, MethodImplemented)
	assertMethod(t, "provider/capabilities/read", SurfaceGollemExtension, MethodImplemented)
	assertMethod(t, "tool/list", SurfaceGollemExtension, MethodImplemented)
	assertMethod(t, "cache/stats", SurfaceGollemExtension, MethodImplemented)
	assertMethod(t, "cache/benchmark", SurfaceGollemExtension, MethodImplemented)
	assertMethod(t, "fs/watch", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "fs/unwatch", SurfaceClientRequest, MethodImplemented)
	assertMethod(t, "daemon/status", SurfaceGollemExtension, MethodImplemented)
	assertMethod(t, "daemon/version", SurfaceGollemExtension, MethodImplemented)
	assertMethod(t, "daemon/start", SurfaceGollemExtension, MethodImplemented)
	assertMethod(t, "daemon/stop", SurfaceGollemExtension, MethodImplemented)
	assertMethod(t, "daemon/restart", SurfaceGollemExtension, MethodImplemented)
	assertMethod(t, "fs/changed", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "process/outputDelta", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "process/exited", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "command/exec/outputDelta", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "thread/status/changed", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "thread/closed", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "thread/compacted", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "thread/settings/updated", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "thread/goal/updated", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "thread/goal/cleared", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "thread/name/updated", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "deprecationNotice", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "thread/tokenUsage/updated", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "turn/started", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "turn/completed", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "turn/diff/updated", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "item/started", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "item/agentMessage/delta", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "item/reasoning/textDelta", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "item/commandExecution/outputDelta", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "item/fileChange/patchUpdated", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "item/mcpToolCall/progress", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "item/completed", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "cache/benchmark/completed", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "serverRequest/resolved", SurfaceServerNotification, MethodImplemented)
	assertMethod(t, "item/commandExecution/requestApproval", SurfaceServerRequest, MethodImplemented)
	assertMethod(t, "item/fileChange/requestApproval", SurfaceServerRequest, MethodImplemented)
	assertMethod(t, "item/permissions/requestApproval", SurfaceServerRequest, MethodImplemented)
	assertMethod(t, "item/tool/call", SurfaceServerRequest, MethodImplemented)
	assertMethod(t, "item/tool/requestUserInput", SurfaceServerRequest, MethodImplemented)
	assertMethod(t, "mcpServer/elicitation/request", SurfaceServerRequest, MethodImplemented)
	assertMethod(t, "approval/respond", SurfaceGollemExtension, MethodImplemented)
	assertMethod(t, "feedback/upload", SurfaceClientRequest, MethodNotApplicable)
	assertMethod(t, "initialized", SurfaceClientNotification, MethodImplemented)
}

func TestMethodsReturnsCopy(t *testing.T) {
	methods := Methods()
	methods[0].Method = "mutated"
	if IsKnownMethod("mutated") {
		t.Fatal("Methods returned mutable registry storage")
	}
}

func assertMethod(t *testing.T, method string, surface Surface, state MethodState) {
	t.Helper()
	info, ok := LookupMethod(method)
	if !ok {
		t.Fatalf("method %q not registered", method)
	}
	if info.Surface != surface || info.State != state {
		t.Fatalf("%s = surface %s state %s, want %s/%s", method, info.Surface, info.State, surface, state)
	}
}
