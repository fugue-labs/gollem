import type {
  CapabilityRootLocation,
  CollaborationMode,
  CollaborationModeMask,
  ItemPayloadByKind,
  MethodParamsByName,
  MethodResultsByName,
  ModeKind,
  MultiAgentMode,
  ReasoningEffort,
  SelectedCapabilityRoot,
  Settings,
} from "../gollem_appserver_protocol";
import {
  itemPayloadBindings,
  protocolMethods,
  wireTypeBindings,
} from "../gollem_appserver_protocol";

type Equal<A, B> =
  (<T>() => T extends A ? 1 : 2) extends
    (<T>() => T extends B ? 1 : 2)
    ? true
    : false;
type Expect<T extends true> = T;
type ExactMembers<Union, Expected> = Union extends unknown
  ? Equal<Union, Expected> extends true
    ? Union
    : never
  : never;

type ExpectedSettings = {
  model: string;
  reasoning_effort: ReasoningEffort | null;
  developer_instructions: string | null;
};
type ExpectedMode = {
  mode: ModeKind;
  settings: Settings;
};
type ExpectedMask = {
  name: string;
  mode: ModeKind | null;
  model: string | null;
  reasoning_effort: ReasoningEffort | null | null;
};
type ExpectedLocation = {
  type: "environment";
  environmentId: string;
  path: string;
};
type ExpectedRoot = {
  id: string;
  location: CapabilityRootLocation;
};
type CollaborationCapabilityContracts =
  | CapabilityRootLocation
  | CollaborationMode
  | CollaborationModeMask
  | ModeKind
  | MultiAgentMode
  | SelectedCapabilityRoot
  | Settings;

type Contracts = [
  Expect<Equal<ModeKind, "plan" | "default">>,
  Expect<Equal<MultiAgentMode, { custom: string } | "explicitRequestOnly" | "proactive">>,
  Expect<Equal<Settings, ExpectedSettings>>,
  Expect<Equal<CollaborationMode, ExpectedMode>>,
  Expect<Equal<CollaborationModeMask, ExpectedMask>>,
  Expect<Equal<CapabilityRootLocation, ExpectedLocation>>,
  Expect<Equal<SelectedCapabilityRoot, ExpectedRoot>>,
  Expect<Equal<ExactMembers<MethodParamsByName[keyof MethodParamsByName], CollaborationCapabilityContracts>, never>>,
  Expect<Equal<ExactMembers<MethodResultsByName[keyof MethodResultsByName], CollaborationCapabilityContracts>, never>>,
  Expect<Equal<ExactMembers<ItemPayloadByKind[keyof ItemPayloadByKind], CollaborationCapabilityContracts>, never>>,
  Expect<Equal<typeof protocolMethods["length"], 229>>,
  Expect<Equal<typeof wireTypeBindings["length"], 86>>,
  Expect<Equal<typeof itemPayloadBindings["length"], 5>>,
];

({
  mode: "plan",
  settings: {
    model: "",
    reasoning_effort: null,
    developer_instructions: null,
  },
}) satisfies CollaborationMode;
({
  name: "",
  mode: null,
  model: null,
  reasoning_effort: null,
}) satisfies CollaborationModeMask;
({
  id: "",
  location: {
    type: "environment",
    environmentId: "",
    path: "file:///workspace",
  },
}) satisfies SelectedCapabilityRoot;
({ custom: "" }) satisfies MultiAgentMode;

// @ts-expect-error settings optional fields are canonically required nullable.
({ model: "" }) satisfies Settings;
// @ts-expect-error collaboration mode is closed to plan/default.
"code" satisfies ModeKind;
// @ts-expect-error legacy none is accepted only on serde input, not canonical TypeScript.
"none" satisfies MultiAgentMode;
// @ts-expect-error the custom payload is required and non-null.
({ custom: null }) satisfies MultiAgentMode;
// @ts-expect-error capability roots currently permit only environment locations.
({ type: "local", environmentId: "", path: "/workspace" }) satisfies CapabilityRootLocation;
// @ts-expect-error location is required.
({ id: "" }) satisfies SelectedCapabilityRoot;

void (null as unknown as Contracts);
