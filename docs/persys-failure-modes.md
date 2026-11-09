# Persys Compute --- Failure Modes and Recovery Strategy

**Document type:** Architecture / Reliability Design\
**Status:** Design specification

## 1. Purpose

This document defines how Persys behaves when compute nodes, agents,
workloads, or supporting infrastructure fail. It covers failure
detection, workload recovery, returning nodes, reconciliation, garbage
collection, and storage-dependent workloads.

Core principle:

> The Scheduler owns cluster-level decisions and desired state. Agents
> execute local operations, report observed state, and reconcile their
> local runtime with authoritative instructions.

## 2. Terminology

  -----------------------------------------------------------------------
  Term                                Meaning
  ----------------------------------- -----------------------------------
  Node                                A machine providing compute
                                      resources.

  Agent                               The local Persys process that
                                      executes and monitors workloads.

  Scheduler                           The control-plane component that
                                      decides workload placement and
                                      recovery.

  Workload UUID                       Stable identity of a logical
                                      workload, including after
                                      relocation.

  Heartbeat                           Periodic liveness message from an
                                      Agent.

  Desired state                       State the control plane expects the
                                      cluster to reach.

  Observed state                      State reported by an Agent or
                                      runtime.

  Reconciliation                      Comparing desired and observed
                                      state, then correcting differences.

  Garbage Collector (GC)              Cleanup mechanism for stale,
                                      orphaned, or misplaced workloads.

  Fencing                             Preventing an old or isolated node
                                      from continuing as the valid owner
                                      after ownership has moved.

  Ephemeral workload                  A node-bound workload whose local
                                      storage prevents normal relocation.
  -----------------------------------------------------------------------

## 3. Workload recovery classes

### 3.1 Relocatable workloads

A relocatable workload may be scheduled on another healthy node after
its original node is confirmed failed and ownership can be safely
transferred.

Its UUID remains stable: the Scheduler changes its placement/ownership
rather than creating an unrelated logical workload.

Relocation does not automatically preserve in-memory state or
uncommitted local data. Recovery guarantees depend on storage and
application design.

### 3.2 Ephemeral, node-bound workloads

An ephemeral workload is tied to its original node because its required
storage is local and cannot be attached to another healthy node through
the normal relocation process.

Policy:

-   Do not automatically reschedule it elsewhere.
-   Mark it unavailable or blocked pending recovery.
-   SRE must recover the original node, or restore the workload through
    an approved backup/snapshot procedure.
-   Validate data consistency before bringing it back.

Restarting the workload elsewhere without its required local data is not
equivalent to recovering it.

## 4. Node failure detection

Agents periodically send heartbeats to the Scheduler. Missing heartbeats
may indicate node failure, Agent failure, network partition, overload,
or an intermediate network problem.

### 4.1 Five-minute failure window

The current policy uses a five-minute heartbeat-loss window:

1.  Scheduler records the last valid heartbeat.
2.  If no valid heartbeat arrives during the configured five-minute
    window, the node becomes a failure candidate.
3.  Scheduler performs the configured validation and marks the node
    failed/unhealthy when the criteria are met.
4.  Scheduler evaluates workloads assigned to that node and applies each
    workload's recovery class.

The five-minute value should be configurable and account for heartbeat
interval, network jitter, and control-plane health.

**Important:** A timeout proves that the Scheduler cannot currently
communicate with the node. It does not, by itself, prove that the node
has stopped executing workloads.

## 5. Recovery flow for relocatable workloads

When a node is confirmed failed, the Scheduler processes its assigned
workloads.

1.  **Identify affected workloads:** Read workload UUIDs and current
    placement from authoritative cluster state.
2.  **Classify workloads:** Determine whether each is relocatable or
    ephemeral/node-bound.
3.  **Establish safe ownership transfer:** Ensure the old node cannot
    continue as the valid owner before a replacement instance is allowed
    to run. See Section 8.
4.  **Revoke old placement:** Persist a termination/cleanup instruction
    or tombstone in etcd, associated with the workload UUID and the old
    ownership generation.
5.  **Select a destination:** Choose a healthy eligible node based on
    resources, capabilities, labels, requirements, and health.
6.  **Assign the workload:** Update authoritative desired placement and
    ownership generation.
7.  **Instruct the destination Agent:** Send the execution instruction
    to the selected node.
8.  **Observe and confirm:** Agent reports the result; Scheduler
    reconciles desired and observed state.

### 5.1 UUID and ownership generation

The UUID identifies the logical workload, not a particular process
instance or node. Track an ownership generation, epoch, or fencing token
as well.

Example:

-   Workload UUID: `W-123`
-   Previous owner: `node-A`, generation `7`
-   New owner: `node-B`, generation `8`

The old generation must no longer be accepted as authoritative. This
protects against delayed commands and stale status reports.

## 6. Recovery of a previously failed node

A node may reconnect after its relocatable workloads have already been
started elsewhere. It must not simply resume its old workloads.

### 6.1 Reconciliation sequence

1.  Agent reconnects and establishes an authenticated connection.
2.  Scheduler identifies the node and checks its current membership and
    ownership status.
3.  Agent reports its local workload inventory, including UUID, runtime
    state, and ownership generation.
4.  Control plane compares the inventory with authoritative desired
    state and current placement in etcd.
5.  If a workload UUID has been revoked on the returning node and is now
    assigned elsewhere, the old local instance is stale.
6.  Agent receives/applies the authoritative cleanup instruction and
    terminates the stale workload.
7.  Agent reports the cleanup result.
8.  Reconciliation continues until local state matches desired state.

### 6.2 Example

Before failure, `W-123` runs on `node-A`. After failure, Scheduler
revokes the old ownership, assigns `W-123` to `node-B` with a newer
generation, and starts it there.

When `node-A` reconnects, its Agent reports that it still has `W-123`.
The control plane sees that `node-A` no longer owns the current
generation, so the old instance is terminated and removed.

## 7. Agent Garbage Collection

Agent GC is a **secondary safety mechanism**, not a replacement for
Scheduler-driven ownership and reconciliation.

GC periodically inspects local workloads. A workload is a cleanup
candidate if:

-   Its UUID is no longer assigned to the local node.
-   Its ownership generation is stale.
-   The control plane marks it terminated or revoked.
-   It is an orphan absent from authoritative desired state.

GC terminates and removes the stale local instance, then reports the
result.

### 7.1 Reconciliation versus GC

-   **Reconciliation** compares observed state with authoritative
    desired state and drives convergence.
-   **GC** is a local cleanup backstop for stale or orphaned runtime
    objects.

If normal reconciliation cleanup does not complete, GC may eventually
detect and remove the stale workload, provided it can obtain
sufficiently fresh authoritative ownership information.

**Safety requirement:** If the Agent cannot verify current ownership
because the control plane or etcd is unavailable, it must not guess that
a workload is stale and delete it. Destructive cleanup should be
deferred until authoritative state can be verified.

## 8. Split-brain and duplicate-execution protection

This is a critical correctness requirement.

A node may lose connectivity to the Scheduler while still running its
workloads. If Scheduler waits five minutes and starts the same workload
elsewhere, both copies may run simultaneously.

Writing a termination instruction to etcd does **not** physically stop
an unreachable node. The returning Agent can act on that instruction
later, but this alone does not prevent overlap during the network
partition.

Persys therefore needs a fencing strategy before treating ownership as
safely transferred. Possible mechanisms include:

-   **Fencing tokens / monotonically increasing ownership generations:**
    Every assignment has a newer generation. Components that enforce
    ownership reject operations from stale generations.
-   **Storage-level fencing:** Where supported, revoke the old node's
    access to shared writable storage before granting access to the new
    owner.
-   **Infrastructure fencing:** Use an out-of-band mechanism to power
    off or isolate the old node where appropriate.
-   **Application-level idempotency:** For workloads with external side
    effects, use idempotency keys or transactional deduplication where
    possible.

A fencing token only works if the execution path, storage layer, or
downstream service checks it. Merely storing a generation in etcd does
not stop an old process from continuing to run.

If Persys cannot establish that the previous owner is fenced, recovery
must be treated as potentially at-least-once. Do not claim exactly-once
execution.

## 9. Ephemeral workload failure policy

When a node hosting an ephemeral, node-bound workload fails:

1.  Scheduler detects and validates the node failure.
2.  Scheduler identifies the workload as non-relocatable.
3.  Workload is marked unavailable/blocked; it is not automatically
    assigned elsewhere.
4.  Scheduler records the failure and exposes the required recovery
    action to operators.
5.  SRE investigates and attempts to restore the original node and its
    local storage.
6.  If the original node cannot be recovered, SRE follows the approved
    backup/snapshot restoration procedure, if available.
7.  Workload returns only after storage and consistency requirements are
    satisfied.

Recovery options:

-   **Original node recovery:** Restore the machine and verify local
    storage.
-   **Backup/snapshot recovery:** Restore data to an approved target
    using a controlled procedure.
-   **Permanent data loss:** If neither node nor valid backup can be
    recovered, report the workload as unrecoverable and follow the
    incident/data-loss process.

A snapshot is not automatically a consistent backup. Define snapshot
frequency, consistency guarantees, retention, restore testing, and
acceptable RPO/RTO.

## 10. Failure-mode matrix

  -----------------------------------------------------------------------
  Failure mode            Detection / signal      Expected Persys
                                                  behaviour
  ----------------------- ----------------------- -----------------------
  Agent process crashes,  Heartbeats stop; node   Mark Agent unavailable
  node remains healthy    may remain reachable    after timeout;
                                                  determine whether
                                                  workloads still run
                                                  before recovery. Agent
                                                  crash does not prove
                                                  all workloads stopped.

  Entire node becomes     Heartbeats stop and     Validate failure; apply
  unavailable             infrastructure checks   workload policy.
                          fail                    Relocate eligible
                                                  workloads only after
                                                  safe ownership
                                                  transfer/fencing.

  Temporary network       Scheduler cannot        Treat ownership as
  partition               receive heartbeats,     uncertain; prevent
                          node may still execute  split-brain. Timeout
                                                  alone is not proof old
                                                  execution stopped.

  Returning node after    Agent reconnects and    Reconcile against etcd;
  relocation              reports local inventory terminate stale
                                                  workloads whose
                                                  ownership was revoked
                                                  or moved.

  Scheduler               Scheduler process       Recover authoritative
  crashes/restarts        unavailable             state from etcd and
                                                  operational state from
                                                  appropriate stores;
                                                  resume reconciliation
                                                  before conflicting
                                                  assignments.

  etcd unavailable        Authoritative state     Avoid unsafe ownership
                          cannot be read or       changes and destructive
                          updated                 cleanup based on stale
                                                  state. Restore
                                                  service/quorum, then
                                                  reconcile.

  Redis unavailable       Ephemeral/high-churn    Apply Redis recovery
                          operational state may   policy. Do not treat
                          be unavailable          Redis as authority for
                                                  durable workload
                                                  ownership.

  Destination Agent       Agent reports           Scheduler selects
  rejects execution due   structured rejection    another eligible node
  to insufficient                                 or reports insufficient
  resources                                       capacity. Agent does
                                                  not move the workload
                                                  itself.

  Old workload survives   Local inventory         Reconciliation removes
  on returning node       conflicts with current  it; Agent GC is a
                          placement               secondary cleanup
                                                  backstop.

  GC fails or cannot      Cleanup error or        Keep condition visible
  confirm ownership       control-plane state     and retry safely. Do
                          unavailable             not delete based on
                                                  unverified assumptions.

  Ephemeral workload's    Node failure plus       Do not relocate
  node fails              node-bound storage      automatically. SRE
                          policy                  restores node or
                                                  performs approved
                                                  backup/snapshot
                                                  recovery.

  Destination node fails  New owner becomes       Re-evaluate current
  during recovery         unavailable             ownership generation
                                                  and repeat recovery
                                                  safely; reject stale
                                                  reports.

  Workload process        Runtime/Agent reports   Apply workload
  crashes while node      process exit            restart/retry policy;
  remains healthy                                 distinguish process
                                                  failure from node
                                                  failure.

  Duplicate or delayed    Report references old   Reject or ignore stale
  status report arrives   assignment/generation   status updates; only
                                                  current generation
                                                  updates authoritative
                                                  state.
  -----------------------------------------------------------------------

## 11. State and data ownership

### etcd --- authoritative cluster state

Use etcd for durable authoritative coordination state, such as:

-   Node membership and node status, as defined by the control-plane
    model.
-   Workload desired state and current placement/ownership.
-   Ownership generations, revocations, and cleanup/tombstone
    instructions.
-   State required to reconstruct Scheduler decisions after restart.

### Redis --- high-churn operational data

Use Redis for ephemeral or high-churn operational information, such as:

-   Events and retry scheduling data.
-   Short-lived operational state and caches.
-   Other frequently updated data that does not define authoritative
    workload ownership.

Document Redis persistence and rebuild policies separately. Redis must
not become a second, conflicting authority for workload placement.

## 12. Observability and operational requirements

Recommended metrics and events:

-   Last heartbeat timestamp and heartbeat age per node.
-   Node state transitions and failure-detection duration.
-   Workloads affected by each node failure.
-   Recovery attempts and outcomes by workload class.
-   Time from failure detection to replacement workload readiness.
-   Fencing and ownership-transfer success/failure.
-   Stale workloads found and removed by reconciliation or GC.
-   GC failures and cleanup retry counts.
-   Ephemeral workloads awaiting SRE intervention.
-   Backup/snapshot restore outcomes and recovery duration.

Logs should include node ID, workload UUID, ownership generation,
operation ID, failure reason, and reconciliation/GC action where
applicable.

## 13. Required invariants

1.  A workload UUID identifies the logical workload across relocation.
2.  Only the current valid ownership generation may control placement
    and authoritative status.
3.  An Agent never independently chooses another node for a workload.
4.  A node is not assumed to have stopped executing solely because
    heartbeats disappeared.
5.  Relocatable workloads are not started elsewhere until old ownership
    is safely fenced according to the workload's guarantees.
6.  A returning node must reconcile before stale workloads are treated
    as valid cluster workloads.
7.  GC uses authoritative, sufficiently fresh state and fails safe when
    ownership cannot be verified.
8.  Ephemeral node-bound workloads are never automatically relocated.
9.  Loss of node-local disk is a data-recovery incident, not ordinary
    rescheduling.
10. Stale reports and delayed commands must not overwrite newer
    ownership or state.

## 14. Open design decisions

-   Is the five-minute timeout measured from the last heartbeat? What
    validation is required before declaring failure?
-   What exact mechanism fences an old node during a network partition?
-   Does etcd store a tombstone/revocation record, desired-state change,
    or durable command object? Define schema, lifecycle, and
    acknowledgement semantics.
-   How are ownership generations checked by Agents, runtimes, storage,
    and downstream services?
-   What happens if the destination Agent starts a workload but its
    success report is lost?
-   Which workloads are safe to retry, and which require idempotency or
    application-level recovery?
-   How are ephemeral workload backups/snapshots created, tested,
    retained, and restored?
-   What are the RPO and RTO targets for each recovery class?
-   How are stale commands and reports handled after Scheduler restart?
-   What is the retry/escalation policy when reconciliation or GC cannot
    complete?

## 15. Summary

Persys combines heartbeat-based failure detection, Scheduler-controlled
recovery, Agent-driven execution, reconciliation, and garbage collection
to keep cluster state consistent.

For relocatable workloads, Scheduler revokes the old placement, safely
transfers ownership, and schedules the same logical workload UUID on a
healthy node. If the old node returns, its Agent reports its local
inventory and reconciliation removes stale instances. Agent GC is a
secondary cleanup mechanism.

Ephemeral workloads with node-local storage are not automatically moved.
SRE must recover the original node or perform a controlled restore from
a valid backup or snapshot.

The most important safety condition is:

> A heartbeat timeout is not proof that the old workload has stopped.
> Ownership transfer must be protected against split-brain and duplicate
> execution through a real fencing mechanism---not merely a database
> record or delayed cleanup.
