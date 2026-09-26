const PUBLIC_SELECTION = ["dist", "README.md", "LICENSE"];
const PUBLIC_ROOT_FILES = ["package.json", "README.md", "LICENSE"];
const REQUIRED_EXPORTS = [".", "./web"];
const REQUIRED_CONDITIONS = ["types", "import", "require"];
const RECEIPT_FUNCTION_EXPORTS = [
  "mintReceiptOperationContext",
  "parseGroupID",
  "parseOperationID",
  "parseReceiptOperationContext",
  "ReceiptMutationUncertainError",
  "ReceiptReconciliationError",
];
const RECEIPT_NUMBER_EXPORTS = {
  CONTRIB_ID_BYTES: 24,
  RECEIPT_EPOCH_BYTES: 16,
  RECEIPT_GENERATION_BYTES: 16,
  RECEIPT_GROUP_ID_BYTES: 16,
  RECEIPT_NODE_ID_BYTES: 16,
  RECEIPT_OPERATION_ID_BYTES: 49,
  RECEIPT_POLICY_FINGERPRINT_BYTES: 32,
  RECEIPT_STATUS_MAX_ITEMS: 10_000,
};
const RECEIPT_METHODS = [
  "getReceiptCapability",
  "getReceiptStatuses",
  "getReceiptStatus",
  "putVerticesWithReceipt",
  "putVerticesIfAbsentWithReceipt",
  "putVertexWithReceipt",
  "putVertexIfAbsentWithReceipt",
  "deleteVerticesWithReceipt",
  "deleteVertexWithReceipt",
  "deleteEdgesWithReceipt",
  "deleteEdgeWithReceipt",
  "addEdgesWithReceipt",
  "addEdgeWithReceipt",
];
const REPO_ONLY_DIRECTORIES = new Set([
  "src",
  "test",
  "tests",
  "__tests__",
  "scripts",
  "example",
  "examples",
  "node_modules",
  ".github",
  ".git",
  ".ssh",
  ".aws",
  "secrets",
  "credentials",
]);
const SECRET_FILE =
  /^(?:\.env(?:\..*)?|\.npmrc|\.netrc|id_(?:rsa|ed25519)|.*(?:secret|credential|private[-_.]?key).*|.*\.(?:pem|key|p8|p12|pfx))$/i;

export function assertPackageArchive(manifest, entries, gitRef) {
  if (manifest === null || typeof manifest !== "object" || Array.isArray(manifest)) {
    throw new Error("npm pack package.json must be an object");
  }
  if (!Array.isArray(entries)) {
    throw new Error("npm pack archive entries must be an array");
  }

  const errors = [];
  if (manifest.name !== "lantern-sdk") {
    errors.push('npm pack package name must be "lantern-sdk"');
  }
  if (typeof manifest.version !== "string" || !manifest.version) {
    errors.push("npm pack package version is required");
  }
  if (gitRef?.startsWith("refs/tags/") && gitRef !== `refs/tags/sdks/node/v${manifest.version}`) {
    errors.push(`npm pack version ${manifest.version} does not match release tag ${gitRef}`);
  }
  if (!Array.isArray(manifest.files) || manifest.files.some((file) => typeof file !== "string")) {
    errors.push("package.json files must select dist, README.md, and LICENSE");
  } else {
    const selected = new Set(manifest.files);
    for (const file of PUBLIC_SELECTION) {
      if (!selected.has(file)) errors.push(`package.json files is missing "${file}"`);
    }
    for (const file of selected) {
      if (!PUBLIC_SELECTION.includes(file)) errors.push(`package.json files disallows "${file}"`);
    }
  }
  if (manifest.engines?.node !== ">=20") {
    errors.push('package.json engines.node must be ">=20"');
  }
  if (manifest.type !== "module") {
    errors.push('package.json type must be "module" for ESM .js exports');
  }

  const packed = new Set();
  for (const entry of entries) {
    if (typeof entry !== "string" || !entry.startsWith("package/")) {
      errors.push(`invalid npm pack entry: ${JSON.stringify(entry)}`);
      continue;
    }
    const path = entry.slice("package/".length);
    const segments = path.split("/");
    if (
      !path ||
      path.includes("\\") ||
      segments.some((segment) => !segment || segment === "." || segment === "..")
    ) {
      errors.push(`invalid npm pack path: ${JSON.stringify(entry)}`);
      continue;
    }
    if (packed.has(path)) errors.push(`duplicate npm pack entry: ${path}`);
    packed.add(path);
    if (
      segments.some((segment) => REPO_ONLY_DIRECTORIES.has(segment.toLowerCase())) ||
      segments.some((segment) => SECRET_FILE.test(segment))
    ) {
      errors.push(`forbidden npm pack entry: ${path}`);
    } else if (!PUBLIC_ROOT_FILES.includes(path) && !path.startsWith("dist/")) {
      errors.push(`unexpected npm pack entry: ${path}`);
    }
  }
  for (const file of PUBLIC_ROOT_FILES) {
    if (!packed.has(file)) errors.push(`missing public npm pack file: ${file}`);
  }

  const exports = manifest.exports;
  if (exports === null || typeof exports !== "object" || Array.isArray(exports)) {
    errors.push("package.json exports must declare . and ./web");
  } else {
    for (const specifier of REQUIRED_EXPORTS) {
      const conditions = exports[specifier];
      if (conditions === null || typeof conditions !== "object" || Array.isArray(conditions)) {
        errors.push(`missing package.json export "${specifier}" with types, import, and require`);
        continue;
      }
      for (const condition of REQUIRED_CONDITIONS) {
        if (typeof conditions[condition] !== "string" || !conditions[condition]) {
          errors.push(`missing package.json export "${specifier}" condition "${condition}"`);
        }
      }
    }
    for (const [specifier, conditions] of Object.entries(exports)) {
      if (conditions === null || typeof conditions !== "object" || Array.isArray(conditions)) {
        if (!REQUIRED_EXPORTS.includes(specifier)) {
          errors.push(`package.json export "${specifier}" must declare file conditions`);
        }
        continue;
      }
      for (const [condition, target] of Object.entries(conditions)) {
        const label = `export "${specifier}" condition "${condition}"`;
        if (typeof target !== "string" || !target.startsWith("./dist/")) {
          errors.push(`package.json ${label} must target ./dist/`);
          continue;
        }
        const path = target.slice(2);
        if (path.split("/").some((segment) => !segment || segment === "." || segment === "..")) {
          errors.push(`package.json ${label} has an invalid path: ${target}`);
        } else if (!packed.has(path)) {
          errors.push(`missing ${label} target in npm pack: ${path}`);
        }
        if (condition === "types" && !/\.d\.(?:ts|cts|mts)$/.test(target)) {
          errors.push(`package.json ${label} must target declarations: ${target}`);
        }
        if (condition === "import" && !/\.(?:js|mjs)$/.test(target)) {
          errors.push(`package.json ${label} must target ESM: ${target}`);
        }
        if (condition === "require" && !/\.cjs$/.test(target)) {
          errors.push(`package.json ${label} must target CJS: ${target}`);
        }
      }
    }
    for (const [field, condition] of [
      ["main", "require"],
      ["module", "import"],
      ["types", "types"],
    ]) {
      if (typeof manifest[field] !== "string" || manifest[field] !== exports["."]?.[condition]) {
        errors.push(`package.json ${field} must match export "." condition "${condition}"`);
      }
    }
  }
  if (errors.length) {
    throw new Error(`Invalid npm pack candidate:\n- ${errors.join("\n- ")}`);
  }
}

export function assertReceiptPackageExports(runtime, entrypoint) {
  if (entrypoint !== "node" && entrypoint !== "web") {
    throw new Error(`unknown npm pack entrypoint: ${entrypoint}`);
  }
  const specifier = entrypoint === "node" ? "lantern-sdk" : "lantern-sdk/web";
  const errors = [];
  const connect = entrypoint === "node" ? "connect" : "connectWeb";
  if (typeof runtime?.[connect] !== "function") {
    errors.push(`missing function export ${connect}`);
  }
  for (const name of RECEIPT_FUNCTION_EXPORTS) {
    if (typeof runtime?.[name] !== "function") {
      errors.push(`missing function export ${name}`);
    }
  }
  for (const [name, expected] of Object.entries(RECEIPT_NUMBER_EXPORTS)) {
    if (runtime?.[name] !== expected) {
      errors.push(`expected ${name}=${expected}`);
    }
  }
  if (typeof runtime?.Lantern !== "function" || !runtime.Lantern.prototype) {
    errors.push("missing Lantern class");
  } else {
    for (const method of RECEIPT_METHODS) {
      if (typeof runtime.Lantern.prototype[method] !== "function") {
        errors.push(`missing Lantern.${method}`);
      }
    }
  }
  if (errors.length) {
    throw new Error(`Invalid npm pack receipt surface for ${specifier}:\n- ${errors.join("\n- ")}`);
  }
}
