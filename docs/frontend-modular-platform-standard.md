# Frontend Modular Platform Architecture Standard

## 1. Status and scope

This document defines the target architecture and mandatory engineering rules for
the MongoJSON frontend. It applies to every user-facing tool, the application
shell, shared UI, browser-side platform services, and frontend-to-backend API
clients.

The normative keywords **MUST**, **MUST NOT**, **SHOULD**, **SHOULD NOT**, and
**MAY** indicate requirement strength.

This standard replaces the informal terms "socket-style" and "drawer-style"
with explicit, testable architecture properties.

## 2. Architecture position

### 2.1 Near-term target

MongoJSON SHALL evolve into a **governed modular monolith with a microkernel
(plug-in) architecture**:

- A small **Application Shell** owns composition and platform-wide concerns.
- Each business capability is a **Feature Module** organized as a vertical slice.
- Modules connect to the shell through declared **Extension Points**.
- Modules communicate through versioned **Capability Contracts**, not private
  imports or hard-coded knowledge of another module.
- A single deployable frontend remains the default while module boundaries are
  enforced automatically.

This target provides low operational complexity while making modules
configurable, removable, independently testable, and ready to extract.

### 2.2 What this is not

- Route-level `lazy()` loading alone is code splitting, not a plug-in architecture.
- A feature flag hides or changes behavior; it does not by itself remove code,
  dependencies, routes, state, or backend requirements.
- A feature folder is not an independent module unless its public API and allowed
  dependencies are enforced.
- Micro-frontends are not the immediate target. They become appropriate only when
  independent build and deployment are real product or team requirements.

### 2.3 Future option

If independent deployment becomes necessary, an extractable Feature Module MAY
be promoted to a **Micro-Frontend** and composed at runtime through a supported
mechanism such as Module Federation. Promotion MUST preserve the module contract;
it MUST NOT require the shell to learn the module's internal implementation.

### 2.4 Requirement mapping

| Informal requirement | Professional term | Required evidence |
| --- | --- | --- |
| "Socket-style" composition | Pluggability and composability | One manifest registers the module through stable extension points; the shell has no knowledge of module internals. |
| Switch a module on or off | Configurable activation | Build profile, startup policy, or runtime feature gate disables both route and navigation, with a passing disabled-module build. |
| Delete without affecting other tools | Removability and optional dependency safety | Removing the module and its registration leaves all unrelated builds, tests, and routes passing. |
| Move into an independent project | Extractability and standalone operability | The same domain implementation runs in a standalone harness by replacing only declared platform adapters. |
| Release independently at runtime | Independent deployability | Separate build and deployment pipeline plus a versioned runtime integration contract; this is the Micro-Frontend level. |

## 3. Terminology

| Term | Definition |
| --- | --- |
| Application Shell (Host) | Stable composition root that owns routing integration, navigation regions, error boundaries, global accessibility, and platform service startup. |
| Feature Module | Cohesive vertical slice that owns its UI, state, domain types, API adapter, styles, tests, and module manifest. |
| Shared Kernel | Small, domain-neutral code shared by modules, such as buttons, panels, editor adapters, HTTP primitives, and contract types. |
| Platform Service | Host-provided capability such as routing, telemetry, persistence, notifications, feature evaluation, or command dispatch. |
| Module Manifest | Declarative public metadata used by the shell to discover, validate, enable, load, and render a module. |
| Extension Point | Named host-owned location or lifecycle hook to which a module may contribute UI or behavior. |
| Capability Contract | Versioned interface through which a module provides or consumes behavior without importing another module's internals. |
| Feature Gate | Policy evaluation that decides whether a registered module or capability is enabled in a given context. |
| Build Profile | Static list of modules included in a build artifact. Excluded modules must not enter the dependency graph or output bundle. |
| Extractable Module | Module that can be moved into its own package or repository without changing its domain implementation. |
| Micro-Frontend | Independently deliverable frontend application composed into a larger product. Independent deployment is mandatory for this term. |

## 4. Required architecture properties

Every Feature Module MUST satisfy all of the following.

### 4.1 Cohesion and ownership

- A module MUST represent one user-visible business capability or bounded context.
- It MUST own its domain UI, state, types, API adapter, styles, tests, and assets.
- Domain-specific code MUST NOT be placed in the Shared Kernel.
- A module MAY contain subfeatures, but those subfeatures MUST remain private
  unless separately exposed through its public API.

### 4.2 Explicit public API

- Each module MUST expose one public entry point, normally `index.ts`.
- Consumers MUST NOT deep-import files below another module's public entry point.
- Public contracts MUST be typed and documented.
- Extracted or separately published contracts MUST use Semantic Versioning.

### 4.3 Dependency direction

Allowed dependency directions are:

```text
application-shell -> module manifests + platform contracts
feature-module    -> shared-kernel + platform contracts + declared contracts
shared-kernel     -> third-party libraries + domain-neutral utilities
```

The following are prohibited:

```text
shared-kernel  -> feature-module
feature A      -> feature B internals
feature module -> application-shell implementation
```

The module dependency graph MUST be acyclic. Any feature-to-feature dependency
MUST be declared in the manifest and expressed through a Capability Contract.

### 4.4 Replaceability and removability

A module is removable only when all of these are true:

1. Its package or directory can be deleted.
2. Its manifest registration can be deleted or excluded.
3. The remaining application builds, tests, and starts successfully.
4. No navigation item, route, title, shell slot, CSS rule, persisted key, or
   backend call remains active for the removed module.
5. Consumers of optional capabilities degrade deliberately instead of failing.

Removal MUST NOT require edits inside unrelated Feature Modules. A required
dependency MAY prevent removal, but that dependency MUST be declared and rejected
by configuration validation with a clear error.

### 4.5 Extractability

A module is extractable when it can run as a standalone application by supplying
adapters for its declared platform contracts. Extraction MUST NOT require changes
to domain components, domain state, or domain services.

An extractable module MUST provide:

- a public package entry point;
- a standalone development entry point or harness;
- an explicit dependency list;
- typed platform ports and default adapters;
- scoped styles and assets;
- build, unit test, and smoke-test commands;
- documented backend capabilities and configuration.

## 5. Composition model

### 5.1 Module manifest

The shell MUST derive routes, navigation, page metadata, feature evaluation, and
shell contributions from one manifest. These concerns MUST NOT be maintained in
parallel hard-coded lists.

Recommended contract:

```ts
export type WorkspaceId = 'tools' | 'documents' | 'entertainment' | 'steward'

export type ToolModuleManifest = {
  id: string
  version: string
  title: string
  workspace: WorkspaceId
  route: {
    path: string
    legacyPaths?: string[]
    load: () => Promise<{ default: React.ComponentType }>
  }
  navigation?: {
    label: string
    icon: React.ComponentType
    order: number
  }
  gate?: string
  provides?: CapabilityId[]
  requires?: Array<{ capability: CapabilityId; optional: boolean }>
  shellSlots?: ShellContribution[]
  backend?: BackendRequirement[]
  standalone?: {
    supported: boolean
  }
}
```

The manifest is metadata and integration wiring. It MUST NOT contain domain
business logic.

Workspaces are a separate presentation contract. The workspace registry owns the
label, description, default module, semantic theme, and ordered module list for
each workspace. A module belongs to exactly one workspace, and its primary route
must use that workspace's route prefix. Legacy paths are compatibility redirects
only and must preserve query strings and hashes.

Shared accessible interaction primitives SHOULD use Base UI (`@base-ui/react`)
instead of duplicating focus management, keyboard navigation, dismissal, and
ARIA behavior. Base UI remains an unstyled behavior layer: workspace components
MUST apply the project's semantic tokens and scoped styles rather than introducing
a second visual system. The global workspace launcher is the reference integration.

### 5.2 Registry

The **Module Registry** is the single source of truth for registered modules.
It MUST:

- reject duplicate module IDs, routes, and capability providers;
- reject modules assigned to an invalid workspace or route prefix;
- ensure every enabled workspace has a valid default module;
- validate required capabilities before rendering;
- evaluate gates before constructing routes and navigation;
- load module code only when the module is enabled and requested;
- expose deterministic ordering;
- report configuration errors before the user enters a broken route.

### 5.3 Extension points

The shell MAY expose a small set of versioned extension points, for example:

- `navigation.primary`
- `workspace.route`
- `shell.bottom-player`
- `shell.right-drawer`
- `command.palette`
- `settings.section`

Feature UI MUST enter global shell regions through these extension points. The
shell MUST NOT directly import feature-owned components. Persistent music
controls, for example, belong in declared shell slots rather than hard-coded
imports in `AppShell`.

### 5.4 Lifecycle

The standard lifecycle is:

```text
discovered -> validated -> enabled -> loaded -> mounted
                                      |          |
                                      +------> unmounted -> disposed
```

- `discovered`: manifest is present in the build profile.
- `validated`: IDs, dependencies, contracts, and routes are valid.
- `enabled`: the feature gate permits activation.
- `loaded`: the implementation chunk has been fetched.
- `mounted`: UI and local providers are active.
- `unmounted`: UI is removed and subscriptions are stopped.
- `disposed`: timers, listeners, media, workers, and transient resources are released.

Every module with external resources MUST implement and test cleanup.

## 6. Activation and feature management

The platform distinguishes three mechanisms.

### 6.1 Build-time inclusion

A Build Profile determines which modules are shipped. A module excluded from the
profile MUST NOT be imported by the application graph and SHOULD produce no
feature-owned bundle or asset.

Use this for product editions, standalone distributions, or permanent removal.

### 6.2 Startup-time activation

Deployment configuration MAY enable a shipped module when the application starts.
This is appropriate for environment capabilities, backend availability, or local
installation policy. Invalid combinations MUST fail validation explicitly.

### 6.3 Runtime feature flags

Runtime flags MAY vary behavior by user, device, rollout, or other evaluation
context. Flag evaluation SHOULD use a vendor-neutral interface compatible with
OpenFeature concepts: flag key, default value, provider, and evaluation context.

Rules:

- Every flag MUST have an owner, purpose, default, creation date, and retirement condition.
- A disabled module MUST expose neither route nor navigation entry.
- Direct URL access to a disabled module MUST return a deliberate unavailable or
  not-found state, never a partially mounted page.
- Permission checks and security controls MUST NOT rely only on client-side flags.
- Temporary rollout flags MUST be removed after rollout; permanent configuration
  belongs in a Build Profile or startup policy.

## 7. Inter-module communication

Modules MUST prefer, in order:

1. URL and typed navigation intents for user-visible navigation.
2. Capability Contracts for request/response behavior.
3. Typed application events for decoupled notifications.
4. Shared persistence only through a platform storage service and namespaced keys.

Modules MUST NOT:

- import another module's state store, component, hook, API client, or domain type;
- write another module's storage keys;
- embed another module's route literal;
- use untyped global events or mutable global objects as an integration API.

Cross-module navigation SHOULD use a capability such as
`workspace.open({ capability, intent, payload })`. For example, Inspect should
request a `mongo-json.format` capability rather than navigate directly to a
hard-coded MongoDB JSON route.

## 8. State, providers, APIs, and styles

### 8.1 State and providers

- State is feature-local by default.
- Feature providers MUST be mounted at the feature boundary.
- Root-level providers are reserved for domain-neutral platform services.
- A feature requiring persistent global UI MUST register an extension-point
  contribution and MUST still own its state and cleanup.

### 8.2 API boundaries

- The Shared Kernel MAY expose an HTTP transport, authentication adapter, error
  mapping, retry policy, and tracing hooks.
- Each Feature Module MUST own its endpoint paths, request/response DTOs, and API adapter.
- A shared API client MUST NOT aggregate unrelated business endpoints.
- Backend requirements MUST be declared in the module manifest so activation can
  fail safely when a required service is unavailable.

### 8.3 Types

- Domain types belong to their owning module.
- Shared types MUST represent stable platform concepts, not a convenient dumping ground.
- Identically shaped domain values from different modules SHOULD remain separate
  unless they have the same meaning and lifecycle.

### 8.4 CSS and assets

- Feature styles MUST be scoped through CSS Modules, a feature root selector, or
  an equivalent isolation mechanism.
- Feature CSS MUST NOT target another module's DOM.
- Global CSS is limited to reset, tokens, typography, accessibility utilities,
  shell layout, and documented shared components.
- Removing a module MUST allow its styles and assets to disappear with it.

## 9. Repository shape

Recommended structure:

```text
frontend/src/
  app/
    shell/
    registry/
    routing/
    feature-gates/
  platform/
    contracts/
    http/
    storage/
    events/
  shared/
    ui/
    editor/
  modules/
    inspect/
      api/
      domain/
      ui/
      styles/
      tests/
      manifest.ts
      index.ts
      standalone.tsx
    mongo-json/
    memo-docs/
    music/
    watch-party/
```

Modules MAY later move into workspace packages such as
`frontend/packages/features/<module>`. The same public API and dependency rules
apply before and after that move.

## 10. Conformance and architecture fitness functions

Architecture rules MUST be executable in CI, not only documented.

Required checks:

1. **Boundary lint**: reject forbidden imports and deep imports.
2. **Dependency graph**: reject cycles and undeclared feature dependencies.
3. **Manifest validation**: reject duplicate IDs, routes, capabilities, and invalid requirements.
4. **Disable matrix**: build and smoke-test the application with each optional module disabled.
5. **Removal test**: at least one representative module is physically absent in a CI fixture and the host still builds.
6. **Standalone test**: every extractable module builds and mounts in its harness.
7. **Contract test**: providers and consumers agree on capability contract versions.
8. **Bundle assertion**: a build-excluded module produces no feature chunk or asset.
9. **Cleanup test**: mounted modules release listeners, timers, workers, media, and subscriptions after unmount.

Import boundaries MAY initially be enforced with ESLint `no-restricted-imports`
and dependency-cruiser or Madge. If the repository becomes a larger workspace,
Nx tag-based module-boundary enforcement is an appropriate upgrade.

## 11. Definition of done for a Feature Module

A new module is complete only when:

- [ ] It has one manifest and one public entry point.
- [ ] Route, navigation, title, gate, and shell contributions come from the manifest.
- [ ] It owns its domain types, API adapter, styles, state, tests, and assets.
- [ ] It has no forbidden direct imports.
- [ ] Required and optional capabilities are declared.
- [ ] Disabled direct navigation is handled deliberately.
- [ ] It passes enabled, disabled, and unmount cleanup tests.
- [ ] Its backend and storage requirements are documented.
- [ ] Its removal does not require edits inside unrelated modules.
- [ ] If marked extractable, its standalone harness builds and runs.

## 12. Maturity model

| Level | Name | Exit criteria |
| --- | --- | --- |
| M0 | Page-oriented monolith | Pages and shared files are organized, but boundaries are conventional only. |
| M1 | Modular monolith | Feature ownership and public APIs exist; direct imports are constrained. |
| M2 | Governed plug-in platform | Registry, manifests, gates, extension points, dependency validation, and disable/removal tests exist. |
| M3 | Extractable modules | Selected modules have standalone harnesses and portable platform adapters. |
| M4 | Micro-frontends | Selected modules have independent builds, deployment pipelines, runtime composition, and versioned integration contracts. |

MongoJSON's required target is **M2**, with strategically valuable modules made
**M3-ready**. M4 requires a separate architecture decision record demonstrating
that independent deployment benefits outweigh runtime, governance, testing, and
operational costs.

As of `origin/main` commit `6197ea7` (2026-07-10), the frontend is classified as
**M0 with partial M1 foundations**: routes are lazy-loaded and some reusable cores
exist, but registration, navigation, global contributions, domain types, API
clients, and styles are not governed by enforceable module boundaries.

The `refactor/frontend-modular-platform` branch implements the M2 baseline:

- `frontend/module-catalog.json` is the build-time discovery list.
- `frontend/src/modules/<id>/manifest.ts` is the runtime composition contract.
- Routes, navigation, titles, providers, and shell slots are registry-driven.
- `VITE_INCLUDED_MODULES` creates a build profile; excluded modules contribute no
  route, JavaScript chunk, or feature-owned CSS.
- `VITE_DISABLED_MODULES` disables shipped modules at startup.
- Capability navigation replaces feature-to-feature route imports.
- Feature-owned API clients, domain types, state, and CSS live with each module.
- CI commands enforce imports, catalog validity, disable behavior, and all seven
  single-module build profiles.

The branch is M3-ready through standalone host profiles. Moving a module to a
separate repository still requires publishing or copying the declared Platform,
Shared UI, and Data Core dependencies; independent deployment remains M4 work.

## 13. Migration priorities for the current frontend

1. Introduce the manifest type and a single registry; derive routes, navigation,
   titles, and icons from it.
2. Move each tool into `modules/<id>` with a public entry point and scoped styles.
3. Split the aggregated `types/tooling.ts` and `lib/api/client.ts` by domain;
   retain only neutral primitives in `platform` or `shared`.
4. Replace hard-coded Inspect-to-JSON/Mongo navigation with typed capability intents.
5. Convert music's root provider and player UI into module-owned state plus
   declared shell-slot contributions.
6. Add boundary lint, manifest validation, and the per-module disable build matrix.
7. Add standalone harnesses only after module boundaries pass the M2 checks.

## 14. Decision rules

- Prefer a modular monolith while modules share one release cadence and one owner.
- Extract a package when reuse, ownership, or independent testing justifies a
  stable public API.
- Adopt a micro-frontend only when independent deployment is required and funded.
- Do not share domain models merely to reduce duplication; share stable contracts.
- Do not create a platform abstraction without at least two real consumers or a
  confirmed extraction requirement.
- Exceptions MUST be documented in an Architecture Decision Record with owner,
  rationale, affected modules, risks, and an expiry or review condition.

## 15. Reference basis

- Martin Fowler, [Micro Frontends](https://martinfowler.com/articles/micro-frontends.html)
- webpack, [Module Federation](https://webpack.js.org/concepts/module-federation/)
- Nx, [Enforce Module Boundaries](https://nx.dev/docs/features/enforce-module-boundaries)
- OpenFeature, [Evaluation API](https://openfeature.dev/docs/reference/concepts/evaluation-api/)
- React, [`lazy`](https://react.dev/reference/react/lazy)
- Semantic Versioning, [Semantic Versioning 2.0.0](https://semver.org/)
