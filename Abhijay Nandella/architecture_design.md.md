# Final Architecture Proposal for Integrating the NVIDIA DPF Operator into the OPI DPU Operator

# 1. Executive Summary

This document presents the final architecture for integrating the NVIDIA DPF Operator into the OPI DPU Operator based exclusively on the uploaded repository documentation, GAP Analysis, Architectural Boundary Analysis, Design Space Exploration, and Architecture Review. The selected architecture is the **Adapter Controller** approach, which emerged as the strongest candidate during the architecture review because it best satisfies the documented design objectives while preserving clear architectural boundaries.

The repositories establish three distinct architectural layers:

* The **OPI API** provides vendor-neutral control-plane contracts through protobuf definitions and generated APIs. It does not implement Kubernetes controllers or reconciliation. 
* The **OPI DPU Operator** provides Kubernetes-native management of Data Processing Units through Custom Resources, controller-runtime reconciliation, daemon-side runtime components, and vendor abstraction interfaces. 
* The **NVIDIA DPF Operator** provides a comprehensive NVIDIA-specific platform responsible for DPU provisioning, platform lifecycle, networking, storage, service deployment, and remote DPU cluster management using a broad set of Kubernetes controllers and Custom Resources. 

The Design Notes define several architectural constraints that govern the integration:

* The OPI DPU Operator remains the primary user-facing operator.
* NVIDIA DPF functionality should be reused rather than reimplemented.
* Vendor neutrality must be preserved.
* Communication between systems should occur through Kubernetes APIs and Custom Resources.
* Vendor-specific logic should remain isolated.
* Changes to the NVIDIA DPF Operator should be minimized whenever possible. 

The selected Adapter Controller architecture satisfies these constraints by introducing an independent controller responsible solely for translating vendor-neutral OPI resources into NVIDIA DPF resources. The Adapter Controller does not replace or modify the reconciliation responsibilities of either operator. Instead, it serves as a Kubernetes-native integration boundary that preserves the autonomy of both control planes.

Under this architecture:

* The OPI DPU Operator continues to own all OPI Custom Resources and vendor-neutral reconciliation.
* The Adapter Controller observes selected OPI resources and creates or updates corresponding NVIDIA DPF resources.
* The NVIDIA DPF Operator continues to own all NVIDIA-specific resources and performs platform lifecycle management using its existing controllers.
* Status information is synchronized from NVIDIA DPF resources back into OPI resources without transferring controller ownership.
* Resource ownership remains clearly partitioned according to Kubernetes controller-runtime principles.

This architecture maintains the repository-defined separation between generic orchestration and vendor-specific execution while maximizing reuse of the existing NVIDIA platform. It also provides a natural extension model for supporting additional vendors through future adapters without modifying the core OPI reconciliation model.

---

# 2. Design Goals

The design goals are derived directly from the uploaded Design Notes together with the documented architecture of the OPI DPU Operator and NVIDIA DPF Operator repositories. 

## 2.1 Preserve OPI as the Primary User-Facing Operator

The repositories establish the OPI DPU Operator as the vendor-neutral orchestration layer responsible for Kubernetes-native DPU management. The integration must preserve this responsibility and avoid exposing NVIDIA-specific resources directly to users.

Accordingly:

* User workflows continue to interact exclusively with OPI resources.
* Existing OPI APIs remain the primary declarative interface.
* Vendor-specific resources remain internal to the integration.

This preserves the architectural layering identified during the Boundary Analysis. 

---

## 2.2 Reuse Existing NVIDIA DPF Functionality

The NVIDIA DPF Operator already provides extensive functionality including:

* DPU provisioning
* DPU cluster lifecycle
* Service deployment
* Networking
* Storage
* Service chaining
* Remote cluster reconciliation

These capabilities should be reused rather than duplicated inside the OPI Operator. The integration therefore delegates NVIDIA-specific lifecycle management entirely to the existing DPF controllers. 

---

## 2.3 Preserve Vendor Neutrality

The OPI DPU Operator explicitly separates vendor-specific behavior through abstraction interfaces such as the VendorDetector and VendorPlugin mechanisms. Although the selected architecture does not extend these abstractions, it preserves the same design principle by isolating NVIDIA-specific behavior behind an independent integration boundary. 

The resulting architecture ensures that:

* OPI reconciliation remains vendor-neutral.
* NVIDIA implementation details remain encapsulated.
* Future vendor integrations can follow the same architectural pattern.

---

## 2.4 Maintain Clear Controller Ownership

Both repositories follow controller-runtime reconciliation principles in which each controller owns reconciliation for its own Custom Resources.

The integration therefore preserves:

* Independent reconciliation loops.
* Independent controller managers.
* Independent ownership of Custom Resources.
* Independent lifecycle management.

No controller assumes responsibility for resources owned by another operator. This aligns with the controller boundaries identified during the Architectural Boundary Analysis. 

---

## 2.5 Use Kubernetes APIs as the Integration Boundary

The Design Notes explicitly state that communication between operators should occur through Kubernetes APIs and Custom Resources rather than direct internal dependencies. 

The architecture therefore avoids:

* Shared internal packages
* Cross-process API calls
* Direct controller invocation
* Internal library dependencies

Instead, Kubernetes resources become the stable contract between the two operators.

---

## 2.6 Minimize Changes to Existing Codebases

The repositories already contain mature controller implementations with clearly separated responsibilities.

The proposed integration therefore minimizes modifications by:

* Leaving OPI reconciliation unchanged.
* Leaving NVIDIA DPF reconciliation unchanged.
* Introducing integration as an additional controller rather than modifying existing controller logic.

This minimizes maintenance effort and improves upstream acceptability.

---

## 2.7 Follow Kubernetes Operator Best Practices

The design follows the controller-runtime model documented by both repositories.

Key objectives include:

* Declarative reconciliation
* Idempotent reconciliation
* Independent controller ownership
* Status-driven progress
* Finalizer-based cleanup
* Explicit resource ownership
* Condition-based readiness

These principles are consistently reflected throughout both operators.

---

# 3. Architectural Principles

The final architecture is governed by a small set of architectural principles that are consistently supported by the uploaded repositories.

## Principle 1 — Separation of Concerns

Each subsystem owns a distinct architectural responsibility.

| Component           | Primary Responsibility                   |
| ------------------- | ---------------------------------------- |
| OPI API             | Vendor-neutral API contract              |
| OPI DPU Operator    | Vendor-neutral Kubernetes reconciliation |
| Adapter Controller  | Resource translation and integration     |
| NVIDIA DPF Operator | NVIDIA platform lifecycle management     |

No component assumes responsibilities already owned by another subsystem.

---

## Principle 2 — Independent Reconciliation Domains

Each controller-runtime manager remains autonomous.

Specifically:

* OPI reconciles OPI resources.
* Adapter reconciles translation resources.
* DPF reconciles NVIDIA resources.

No reconciliation loop directly executes another controller's business logic.

---

## Principle 3 — Resource Ownership is Explicit

Every Kubernetes resource has exactly one owning controller.

Ownership is never shared.

This principle avoids:

* conflicting reconciliation
* competing updates
* undefined lifecycle management
* ownership ambiguity

---

## Principle 4 — Vendor Isolation

Vendor-specific functionality remains isolated within the NVIDIA DPF Operator.

The OPI Operator never assumes responsibility for:

* NVIDIA provisioning
* NVIDIA networking
* NVIDIA storage
* NVIDIA platform lifecycle

This preserves vendor neutrality while maximizing reuse.

---

## Principle 5 — Kubernetes-Native Communication

All communication occurs through Kubernetes declarative resources.

Controllers exchange desired state through:

* Custom Resources
* Status fields
* Conditions
* Standard reconciliation

The architecture intentionally avoids direct runtime coupling.

---

## Principle 6 — Extensibility Through Composition

The integration introduces composition rather than inheritance.

Future vendor integrations can be added by introducing additional adapters without modifying:

* OPI controller architecture
* NVIDIA controller architecture
* Kubernetes reconciliation model

This aligns with the Design Notes objective of supporting future vendors with minimal additional work. 

---

# 4. Existing Architecture Overview

Before integration, the repositories describe three independent architectural layers.

## OPI API Layer

The OPI API repository defines vendor-neutral control-plane interfaces through protobuf specifications and generated language bindings.

Responsibilities include:

* API contracts
* gRPC services
* REST gateway definitions
* protocol schemas

The repository contains no Kubernetes operator or reconciliation logic. 

---

## OPI DPU Operator Layer

The OPI DPU Operator manages DPU resources through Kubernetes-native reconciliation.

Its architecture includes:

* Controller-runtime manager
* DpuOperatorConfig controller
* DataProcessingUnit controller
* Daemon-side reconciler
* VendorPlugin abstraction
* VendorDetector abstraction
* Template rendering
* Vendor-specific runtime deployment

The operator owns the lifecycle of its own Custom Resources and deploys vendor runtime components while isolating vendor-specific behavior behind abstraction interfaces. 

---

## NVIDIA DPF Platform Layer

The NVIDIA DPF Operator provides a significantly broader platform focused on NVIDIA BlueField infrastructure.

Its controller domains include:

* Platform configuration
* Provisioning
* DPU cluster lifecycle
* Service deployment
* Service chaining
* Networking
* Storage
* Remote reconciliation

The platform follows declarative controller-runtime reconciliation with condition-based status reporting and finalizer-driven cleanup. 

---

## Existing Architectural Boundaries

The Architectural Boundary Analysis identifies clear system boundaries:

* OPI API defines contracts.
* OPI DPU Operator manages vendor-neutral Kubernetes resources.
* NVIDIA DPF Operator manages NVIDIA platform resources.

No implemented integration currently exists between the OPI DPU Operator and the NVIDIA DPF Operator. The Design Notes identify this integration as the architectural objective.

---

# 5. Proposed Architecture Overview

The proposed architecture introduces a dedicated **Adapter Controller** between the OPI DPU Operator and the NVIDIA DPF Operator.

The Adapter Controller serves as the exclusive integration boundary responsible for observing selected OPI resources, creating corresponding NVIDIA DPF resources, monitoring their lifecycle, and synchronizing status back into OPI resources. It does not replace, extend, or bypass the reconciliation logic of either operator. Instead, it composes the two independent control planes using Kubernetes-native declarative interfaces, consistent with the Design Notes and the architectural boundaries identified in the repository analysis.

The resulting architecture consists of four logical layers:

1. **User Interaction Layer** – Users interact exclusively with OPI Custom Resources.
2. **Vendor-Neutral Orchestration Layer** – The OPI DPU Operator reconciles vendor-neutral resources and desired state.
3. **Integration Layer** – The Adapter Controller translates selected OPI resources into NVIDIA DPF resources and synchronizes lifecycle status.
4. **Vendor Platform Layer** – The NVIDIA DPF Operator reconciles NVIDIA-specific resources and manages the complete BlueField platform lifecycle.

This layered organization preserves controller autonomy, explicit ownership, and declarative reconciliation while enabling the OPI Operator to leverage NVIDIA DPF capabilities without duplicating functionality or introducing direct runtime dependencies.


---


# 6. Component Responsibilities

The proposed architecture divides responsibilities across four independent components. Each component owns a clearly defined architectural scope and reconciles only the resources within its ownership boundary. This preserves the controller boundaries identified during the Architectural Boundary Analysis while satisfying the Design Notes' requirement to communicate through Kubernetes APIs and Custom Resources.  

---

## 6.1 OPI DPU Operator

The OPI DPU Operator remains the authoritative vendor-neutral control plane.

Its responsibilities are intentionally unchanged by the proposed integration.

### Primary Responsibilities

* Reconcile OPI Custom Resources.
* Validate user intent.
* Maintain vendor-neutral desired state.
* Deploy and manage existing OPI runtime components.
* Maintain lifecycle of OPI-owned resources.
* Publish vendor-neutral status conditions.
* Remain the primary user-facing operator.

The OPI DPU Operator does **not** create, reconcile, or own NVIDIA DPF resources.

This preserves the existing architectural responsibility defined by the repository. 

---

## 6.2 Adapter Controller

The Adapter Controller introduces the integration layer between the two operators.

Its responsibility is intentionally limited to resource translation and lifecycle synchronization.

### Primary Responsibilities

* Watch selected OPI resources.
* Determine whether NVIDIA DPF resources are required.
* Translate OPI desired state into NVIDIA DPF resources.
* Create or update DPF resources.
* Observe DPF lifecycle.
* Synchronize relevant status into OPI.
* Coordinate deletion sequencing.
* Maintain translation metadata when required.

The Adapter Controller never performs NVIDIA platform operations directly.

Instead, it expresses desired state using DPF Custom Resources and relies on existing DPF reconciliation.

---

### Adapter Does Not Own

The Adapter does not own:

* NVIDIA provisioning
* Networking
* Storage
* Service deployment
* DPU lifecycle
* Remote cluster management

Those remain exclusively within the DPF Operator.

---

### Adapter Responsibilities During Reconciliation

For every observed OPI resource:

1. Read desired state.
2. Determine required DPF resources.
3. Compare desired and existing DPF state.
4. Create missing DPF resources.
5. Update changed DPF resources.
6. Observe DPF status.
7. Reflect appropriate status into OPI.
8. Exit reconciliation.

The Adapter therefore becomes an orchestration bridge rather than a platform controller.

---

## 6.3 NVIDIA DPF Operator

The NVIDIA DPF Operator continues operating exactly as documented in the repository.

Its reconciliation logic remains unchanged.

### Primary Responsibilities

* Provision NVIDIA infrastructure.
* Reconcile DPF Custom Resources.
* Deploy services.
* Manage networking.
* Manage storage.
* Manage service chains.
* Manage DPU clusters.
* Maintain platform status.
* Execute finalizer-driven cleanup.

The DPF Operator remains the authoritative owner of every DPF Custom Resource.

No responsibility is transferred to OPI.



---

## 6.4 Kubernetes API Server

The Kubernetes API Server acts as the integration medium between controllers.

No controller communicates directly with another controller.

Instead:

* OPI writes OPI CRs.
* Adapter watches OPI CRs.
* Adapter writes DPF CRs.
* DPF watches DPF CRs.
* Adapter watches DPF status.
* Adapter updates OPI status.

The Kubernetes API therefore becomes the shared source of truth.

This satisfies the Design Notes requirement that communication occur through Kubernetes APIs rather than internal interfaces. 

---

## 6.5 Responsibility Matrix

| Component           | Responsibilities                                             | Does Not Own        |
| ------------------- | ------------------------------------------------------------ | ------------------- |
| OPI DPU Operator    | Vendor-neutral reconciliation, OPI lifecycle, user interface | NVIDIA lifecycle    |
| Adapter Controller  | Translation, synchronization, orchestration                  | Platform management |
| NVIDIA DPF Operator | NVIDIA platform lifecycle                                    | OPI resources       |
| Kubernetes API      | Declarative state storage                                    | Business logic      |

---

# 7. Resource Ownership Model

A fundamental principle of the proposed architecture is that every Kubernetes resource has exactly one authoritative owner.

This avoids conflicting reconciliation, ownership ambiguity, and undefined lifecycle behavior.

---

## 7.1 Ownership Principles

The ownership model follows five rules.

### Rule 1

A resource has exactly one owning controller.

---

### Rule 2

Controllers never reconcile another controller's resources.

---

### Rule 3

Controllers communicate only through Kubernetes APIs.

---

### Rule 4

Status may be observed by other controllers but ownership is never transferred.

---

### Rule 5

Deletion follows owner responsibility.

---

## 7.2 OPI-Owned Resources

Examples include:

* DpuOperatorConfig
* DataProcessingUnit
* DataProcessingUnitConfig
* ServiceFunctionChain

Ownership:

```
OPI Controller
      │
      ▼
OPI Custom Resources
```

Only OPI reconciliation modifies desired state.

---

## 7.3 Adapter-Owned Resources

The Adapter owns only translation artifacts when such artifacts are required.

Examples include:

* Translation metadata
* Mapping records
* Internal reconciliation state

The Adapter does **not** become the owner of OPI or DPF resources.

---

## 7.4 DPF-Owned Resources

Examples include:

* DPFOperatorConfig
* DPUCluster
* DPUService
* DPUDeployment
* DPUServiceChain
* Provisioning resources
* Networking resources
* Storage resources

Ownership remains entirely within DPF reconciliation.

---

## 7.5 Ownership Diagram

```text
              User

                │

                ▼

      OPI Custom Resources
                │
         Owned by OPI
                │
                ▼
        Adapter Controller
                │
      Creates/Updates
                ▼
      DPF Custom Resources
                │
         Owned by DPF
                │
                ▼
      NVIDIA Platform
```

Ownership changes never occur across layers.

---

## 7.6 Status Ownership

Status ownership follows the same principle.

| Resource | Status Owner |
| -------- | ------------ |
| OPI CR   | OPI Operator |
| DPF CR   | DPF Operator |

The Adapter reads DPF status and updates OPI status without becoming the owner of either resource.

---

## 7.7 Lifecycle Ownership

Lifecycle responsibility remains partitioned.

| Lifecycle Event        | Responsible Component |
| ---------------------- | --------------------- |
| OPI resource creation  | OPI                   |
| Translation            | Adapter               |
| DPF reconciliation     | DPF                   |
| Platform deployment    | DPF                   |
| Status publication     | DPF                   |
| Status synchronization | Adapter               |
| User-facing readiness  | OPI                   |

Each lifecycle phase therefore has a single authoritative owner.

---

# 8. Reconciliation Flow

The complete reconciliation flow consists of three independent reconciliation loops coordinated through Kubernetes declarative state.

No controller invokes another controller directly.

---

## Phase 1 — OPI Reconciliation

The workflow begins when a user creates or updates an OPI Custom Resource.

The OPI controller:

1. Watches the resource.
2. Validates desired state.
3. Ensures required metadata.
4. Applies finalizers if necessary.
5. Stores the desired vendor-neutral state.
6. Updates initial status.

At this point, no NVIDIA-specific resources have been created.

---

## Phase 2 — Adapter Reconciliation

The Adapter observes the updated OPI resource.

Its reconciliation proceeds as follows:

1. Read OPI desired state.
2. Determine corresponding DPF resources.
3. Compare desired and existing DPF resources.
4. Create missing resources.
5. Update changed resources.
6. Record mapping state.
7. Exit reconciliation.

The Adapter does not wait synchronously for DPF reconciliation to complete.

Instead, it relies on subsequent reconciliation triggered by Kubernetes events.

---

## Phase 3 — DPF Reconciliation

The DPF controller detects creation or modification of DPF resources.

It performs its existing reconciliation logic:

* provisioning
* networking
* service deployment
* storage
* platform lifecycle

No changes are required inside DPF reconciliation.

---

## Phase 4 — Status Observation

When DPF updates resource status:

1. Adapter observes status changes.
2. Relevant lifecycle information is translated.
3. OPI status is updated.
4. User observes updated readiness through OPI.

Status therefore propagates upward without transferring ownership.

---

## Complete Reconciliation Sequence

```text
User
 │
 ▼
Create OPI Resource
 │
 ▼
OPI Reconciliation
 │
 ▼
OPI Desired State Stored
 │
 ▼
Adapter Reconciliation
 │
 ▼
Create/Update DPF Resource
 │
 ▼
DPF Reconciliation
 │
 ▼
Platform Provisioned
 │
 ▼
DPF Status Updated
 │
 ▼
Adapter Observes Status
 │
 ▼
OPI Status Updated
```

Each reconciliation loop remains idempotent and independently triggerable.

---

# 9. Controller Interactions

The proposed architecture deliberately minimizes direct interaction between controllers.

Instead of controller-to-controller communication, controllers coordinate through Kubernetes resources and reconciliation events.

---

## 9.1 Interaction Principles

Controller interactions follow four principles:

* Controllers never invoke another controller directly.
* Controllers communicate exclusively through Kubernetes resources.
* Reconciliation remains asynchronous.
* Each controller reacts only to events relevant to resources it watches.

This preserves controller-runtime independence and avoids runtime coupling between the OPI DPU Operator, the Adapter Controller, and the NVIDIA DPF Operator.

---


# 10. Status Synchronization

Status synchronization allows the OPI DPU Operator to remain the user-facing source of truth while leveraging the NVIDIA DPF Operator's existing lifecycle management. The Adapter Controller acts as the synchronization boundary by observing DPF resource status and reflecting relevant information into OPI resources without transferring ownership. This preserves the independent reconciliation domains established by both operators.  

---

## 10.1 Status Principles

The synchronization model follows several principles:

* Each controller owns only the status of its own resources.
* Status flows upward from DPF to OPI.
* The Adapter observes but does not own DPF status.
* OPI exposes a simplified vendor-neutral view.
* Controllers remain loosely coupled.

---

## 10.2 Status Flow

```text
DPF Controller
      │
Updates DPF Status
      │
      ▼
Kubernetes API
      │
      ▼
Adapter Watches Status
      │
      ▼
Translate Relevant Fields
      │
      ▼
Update OPI Status
      │
      ▼
User Views OPI Status
```

The Adapter synchronizes only relevant lifecycle information and does not expose vendor-specific implementation details.

---

## 10.3 Status Responsibilities

| Component | Responsibility                       |
| --------- | ------------------------------------ |
| OPI       | Owns OPI status                      |
| Adapter   | Reads DPF status, updates OPI status |
| DPF       | Owns DPF status                      |

Ownership remains explicit throughout the synchronization process.

---

## 10.4 Synchronization Lifecycle

1. DPF reconciliation completes.
2. DPF updates resource conditions.
3. Kubernetes emits watch events.
4. Adapter observes the change.
5. Adapter translates relevant lifecycle state.
6. Adapter updates the corresponding OPI resource.
7. OPI presents updated readiness to users.

---

## 10.5 Synchronization Characteristics

The synchronization mechanism is:

* Asynchronous
* Event-driven
* Idempotent
* Eventually consistent
* Kubernetes-native

No synchronous RPC or direct controller invocation is required.

---

# 11. Failure Handling

The architecture isolates failures within the component responsible for the failed operation.

No controller attempts to recover by bypassing another controller's ownership.

---

## 11.1 Design Principles

* Failures remain local.
* Reconciliation retries remain independent.
* Controller ownership is never violated.
* Status reflects observed failure.
* Platform recovery relies on normal reconciliation.

---

## 11.2 Failure Scenarios

### Scenario 1 — OPI Validation Failure

```text
User
 │
 ▼
Invalid OPI Resource
 │
 ▼
OPI Validation
 │
 ▼
Status Updated
```

The Adapter is never triggered because valid desired state was not established.

---

### Scenario 2 — Adapter Translation Failure

```text
OPI Resource
      │
      ▼
Adapter
      │
Translation Error
      │
      ▼
Status Updated
```

The DPF Operator is unaffected because no DPF resource is created.

---

### Scenario 3 — DPF Reconciliation Failure

```text
DPF Resource
      │
      ▼
DPF Controller
      │
Failure
      │
      ▼
DPF Status
      │
      ▼
Adapter
      │
      ▼
OPI Status
```

Platform failures remain inside DPF while user-visible status is propagated through OPI.

---

### Scenario 4 — Adapter Restart

Because reconciliation is declarative:

* Existing resources remain unchanged.
* Watches resume.
* Missing reconciliation is replayed.
* Status is eventually synchronized.

No manual recovery is required.

---

## 11.3 Failure Isolation Matrix

| Failure            | Isolated By                 |
| ------------------ | --------------------------- |
| OPI reconciliation | OPI                         |
| Translation        | Adapter                     |
| Platform lifecycle | DPF                         |
| Kubernetes API     | Kubernetes retry mechanisms |

---

# 12. Deletion and Finalizers

Deletion follows the same ownership model as creation.

Each controller cleans up only the resources it owns.

---

## 12.1 Deletion Principles

* Owner deletes owned resources.
* Adapter coordinates sequencing only.
* No controller deletes another controller's resources directly.
* Cleanup remains idempotent.
* Finalizers ensure graceful cleanup.

---

## 12.2 Deletion Flow

```text
Delete OPI Resource
        │
        ▼
OPI Finalizer
        │
        ▼
Adapter Observes Deletion
        │
        ▼
Delete DPF Resource
        │
        ▼
DPF Finalizer
        │
        ▼
Cleanup Platform
        │
        ▼
Remove DPF Finalizer
        │
        ▼
Adapter Completes
        │
        ▼
Remove OPI Finalizer
```

This sequencing preserves controller ownership while allowing graceful cleanup.

---

## 12.3 Cleanup Responsibilities

| Resource                  | Cleanup Owner |
| ------------------------- | ------------- |
| OPI CR                    | OPI           |
| Translation Metadata      | Adapter       |
| DPF CR                    | DPF           |
| NVIDIA Platform Resources | DPF           |

---

# 13. Architecture Diagrams

## 13.1 High-Level Architecture

```text
+-----------------------------+
|          User               |
+-------------+---------------+
              |
              v
+-----------------------------+
|      OPI DPU Operator       |
|  Vendor-Neutral Resources   |
+-------------+---------------+
              |
              v
+-----------------------------+
|     Adapter Controller      |
| Translation & Synchronizer  |
+-------------+---------------+
              |
              v
+-----------------------------+
|   NVIDIA DPF Operator       |
| Platform Lifecycle Manager  |
+-------------+---------------+
              |
              v
+-----------------------------+
| NVIDIA BlueField Platform   |
+-----------------------------+
```

---

## 13.2 Component Interaction

```text
User
 │
 ▼
OPI CR
 │
 ▼
OPI Controller
 │
 ▼
Adapter
 │
 ▼
DPF CR
 │
 ▼
DPF Controller
 │
 ▼
Platform
 │
 ▼
DPF Status
 │
 ▼
Adapter
 │
 ▼
OPI Status
```

---

## 13.3 Controller Ownership

```text
OPI Controller
      │
      ├── OPI Resources

Adapter Controller
      │
      ├── Translation Metadata

DPF Controller
      │
      ├── DPF Resources
      └── NVIDIA Platform
```

Each controller owns only the resources within its responsibility.

---

# 14. Benefits and Trade-Off Analysis

## Benefits

| Benefit                       | Explanation                                                            |
| ----------------------------- | ---------------------------------------------------------------------- |
| Minimal code changes          | Existing OPI and DPF reconciliation remain unchanged.                  |
| Strong separation of concerns | Each component retains a single responsibility.                        |
| High reuse                    | Reuses NVIDIA DPF capabilities instead of duplicating them.            |
| Kubernetes-native             | Communication occurs entirely through Kubernetes APIs and CRs.         |
| Vendor neutrality             | OPI remains vendor-neutral while NVIDIA-specific logic stays isolated. |
| Extensible                    | Additional vendor adapters can follow the same pattern.                |
| Upstream compatibility        | Aligns with the documented design goals and architectural boundaries.  |

---

## Trade-Offs

| Trade-Off              | Impact                                                      |
| ---------------------- | ----------------------------------------------------------- |
| Additional controller  | Introduces one more reconciliation loop to maintain.        |
| Status synchronization | Requires careful mapping between OPI and DPF status models. |
| CR mapping             | Resource equivalence must be maintained as APIs evolve.     |
| Eventual consistency   | Status propagation is asynchronous rather than immediate.   |
| API compatibility      | Adapter must evolve alongside changes in OPI and DPF CRDs.  |

---

# 15. Production Readiness Assessment

Evaluated against the Design Notes criteria:

| Criterion              | Assessment                                                                                                            |
| ---------------------- | --------------------------------------------------------------------------------------------------------------------- |
| Kubernetes correctness | Strong                                                                                                                |
| Reusability            | Strong                                                                                                                |
| Maintainability        | Strong                                                                                                                |
| Extensibility          | Strong                                                                                                                |
| Vendor neutrality      | Preserved                                                                                                             |
| Separation of concerns | Strong                                                                                                                |
| Testability            | High                                                                                                                  |
| Upstream compatibility | Strong                                                                                                                |
| Production readiness   | Suitable, subject to implementation of translation and synchronization logic consistent with repository constraints.  |

---

This completes the full architecture proposal based on the uploaded repository documentation, GAP analysis, boundary analysis, design-space exploration, and architecture review.
