 Design Notes

## Objective

Design an architecture that integrates the NVIDIA DPF Operator into the existing OPI DPU Operator while maximizing reuse of the NVIDIA DPF Operator and preserving the existing OPI architecture.

This document defines the engineering goals and constraints that every proposed solution must satisfy.

---

# Primary Goals

- Integrate NVIDIA support into the OPI DPU Operator.
- Reuse the existing NVIDIA DPF Operator instead of replacing it.
- Preserve OPI's vendor-neutral architecture.
- Follow Kubernetes Operator and controller-runtime best practices.
- Minimize changes to the existing OPI codebase.
- Produce a design that could realistically be accepted by upstream maintainers.

---

# Engineering Constraints

The proposed architecture should:

- Avoid duplicating NVIDIA DPF functionality.
- Avoid forking or modifying the NVIDIA DPF Operator whenever possible.
- Keep vendor-specific logic isolated.
- Support idempotent reconciliation.
- Follow declarative Kubernetes patterns.
- Use standard controller-runtime reconciliation principles.
- Support future vendor integrations (for example AMD) with minimal additional work.
- Remain maintainable over time.

---

# Assumptions

- The OPI DPU Operator remains the primary user-facing operator.
- NVIDIA DPF Operator is already installed and manages NVIDIA-specific resources.
- Communication between operators should occur through Kubernetes APIs and Custom Resources rather than direct internal code dependencies.
- Existing OPI abstractions should be preserved whenever practical.

---

# Questions to Answer

The architecture should clearly answer:

1. Where should NVIDIA integration occur?
2. How should OPI resources be translated into NVIDIA DPF resources?
3. Which operator owns which resources?
4. How should status be synchronized?
5. How should deletion and finalizers be handled?
6. How should failures be propagated?
7. How can additional vendors be supported in the future?

---

# Evaluation Criteria

Every proposed architecture will be evaluated on:

- Kubernetes correctness
- Reusability
- Extensibility
- Maintainability
- Simplicity
- Upstream compatibility
- Separation of concerns
- Testability
- Production readiness

---

# Out of Scope

The design should not:

- Reimplement NVIDIA DPF functionality.
- Modify NVIDIA internals unless absolutely necessary.
- Replace the existing OPI architecture.
- Introduce unnecessary complexity.

---

# Desired Deliverables

The final solution should include:

- Architecture diagram
- Component interaction diagram
- Sequence diagram
- Reconciliation flow
- Trade-off analysis
- Failure scenarios
- Go interface skeleton
- Clear explanation of design decisions

---

# Working Principle

The goal is not to produce the most complex architecture.

The goal is to produce the simplest architecture that is:

- Kubernetes-native
- Easy to maintain
- Easy to extend
- Easy to review
- Suitable for upstream contribution

When multiple solutions exist, prefer the one that minimizes coupling, maximizes reuse, and follows established Kubernetes operator patterns.