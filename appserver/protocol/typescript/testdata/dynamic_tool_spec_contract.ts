import type {
  ItemPayloadByKind,
  MethodParamsByName,
  MethodResultsByName,
} from "../gollem_appserver_protocol";
import {
  itemPayloadBindings,
  protocolMethods,
  wireTypeBindings,
} from "../gollem_appserver_protocol";
import type {
  DynamicToolFunctionSpec,
  DynamicToolNamespaceSpec,
  DynamicToolNamespaceTool,
  DynamicToolSpec,
  JsonValue,
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

type ExpectedFunction = {
  name: string;
  description: string;
  inputSchema: JsonValue;
  deferLoading?: boolean;
};
type ExpectedNamespaceTool = { type: "function" } & ExpectedFunction;
type ExpectedNamespace = {
  name: string;
  description: string;
  tools: Array<ExpectedNamespaceTool>;
};
type ExpectedSpec =
  | ({ type: "function" } & ExpectedFunction)
  | ({ type: "namespace" } & ExpectedNamespace);
type DynamicToolSpecContracts =
  | DynamicToolFunctionSpec
  | DynamicToolNamespaceTool
  | DynamicToolNamespaceSpec
  | DynamicToolSpec;

type Contracts = [
  Expect<Equal<DynamicToolFunctionSpec, ExpectedFunction>>,
  Expect<Equal<DynamicToolNamespaceTool, ExpectedNamespaceTool>>,
  Expect<Equal<DynamicToolNamespaceSpec, ExpectedNamespace>>,
  Expect<Equal<DynamicToolSpec, ExpectedSpec>>,
  Expect<Equal<ExactMembers<MethodParamsByName[keyof MethodParamsByName], DynamicToolSpecContracts>, never>>,
  Expect<Equal<ExactMembers<MethodResultsByName[keyof MethodResultsByName], DynamicToolSpecContracts>, never>>,
  Expect<Equal<ExactMembers<ItemPayloadByKind[keyof ItemPayloadByKind], DynamicToolSpecContracts>, never>>,
  Expect<Equal<typeof protocolMethods["length"], 229>>,
  Expect<Equal<typeof wireTypeBindings["length"], 86>>,
  Expect<Equal<typeof itemPayloadBindings["length"], 5>>,
];

({
  name: "",
  description: "",
  inputSchema: null,
}) satisfies DynamicToolFunctionSpec;
({
  name: " tool ",
  description: " description ",
  inputSchema: { type: "object", properties: { value: { type: "string" } } },
  deferLoading: true,
}) satisfies DynamicToolFunctionSpec;
({
  type: "function",
  name: "tool",
  description: "",
  inputSchema: [],
}) satisfies DynamicToolNamespaceTool;
({
  name: "namespace",
  description: "",
  tools: [],
}) satisfies DynamicToolNamespaceSpec;
({
  type: "namespace",
  name: "namespace",
  description: "",
  tools: [{
    type: "function",
    name: "tool",
    description: "",
    inputSchema: true,
  }],
}) satisfies DynamicToolSpec;

// @ts-expect-error inputSchema is required.
({ name: "tool", description: "" }) satisfies DynamicToolFunctionSpec;
// @ts-expect-error deferLoading is non-null when present.
({ name: "tool", description: "", inputSchema: {}, deferLoading: null }) satisfies DynamicToolFunctionSpec;
// @ts-expect-error namespace tools permit only function variants.
({ type: "namespace", name: "nested", description: "", tools: [] }) satisfies DynamicToolNamespaceTool;
// @ts-expect-error namespace tools are required.
({ name: "namespace", description: "" }) satisfies DynamicToolNamespaceSpec;
// @ts-expect-error the discriminator is required.
({ name: "tool", description: "", inputSchema: {} }) satisfies DynamicToolSpec;
// @ts-expect-error function variants cannot carry namespace tools.
({ type: "function", name: "tool", description: "", inputSchema: {}, tools: [] }) satisfies DynamicToolSpec;
// @ts-expect-error namespace variants cannot carry function input schemas.
({ type: "namespace", name: "namespace", description: "", tools: [], inputSchema: {} }) satisfies DynamicToolSpec;

void (null as unknown as Contracts);
