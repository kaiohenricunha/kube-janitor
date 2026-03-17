# Feature Spec: Resource Classifier

**Status**: Accepted
**Author**: initial
**Date**: 2025-07-17
**Phase**: 1

---

## Summary

The classifier determines what class a Kubernetes resource belongs to by applying
a prioritized chain of classification strategies. Every finding includes the class,
confidence score, and human-readable reasons with evidence.

## Behavioral Specification

### Strategy chain (priority order)

1. **ProtectionStrategy** (highest priority)
   - If the resource is in a protected namespace → ClassProtected (confidence=1.0)
   - If the resource has `janitor.io/protected=true` annotation → ClassProtected
   - If the resource has protection labels → ClassProtected
   - **If classified as Protected, no further strategies run.**

2. **FinalizerStrategy**
   - If the resource has finalizers OTHER than `janitor.io/cleanup` → ClassBlocked
   - The janitor's own finalizer does NOT block classification.

3. **TTLStrategy**
   - If `janitor.io/ttl` annotation is set and elapsed → ClassExpired (confidence=0.9)
   - If policy provides a default TTL and it elapsed → ClassExpired

4. **ExpiresAtStrategy**
   - If `janitor.io/expires-at` annotation (RFC3339) is set and past → ClassExpired

5. **OwnerRefStrategy**
   - For each ownerReference: GET the owner by UID from the API server (not cache)
   - If any live owner found → ClassActive (confidence=0.9)
   - If all owners are dead (NotFound or UID mismatch) → ClassOrphaned (confidence=0.9)
   - If no ownerReferences → ClassUnknown (pass to next strategy)

6. **ResolverStrategy** (lowest priority)
   - Call all resolvers that handle the object kind
   - If any resolver returns live references → ClassActive (confidence=0.9)
   - If no references found → ClassAbandoned (confidence=0.6, heuristic)

### Examples (map directly to test cases)

| # | Scenario | Class | Confidence | Reason Code |
|---|----------|-------|------------|-------------|
| 1 | `janitor.io/protected=true` annotation present | Protected | 0.9+ | `protection-annotation` |
| 2 | Resource in `kube-system` namespace | Protected | 0.9+ | `protected-namespace` |
| 3 | TTL=24h, age=25h | Expired | 0.9+ | `ttl-expired` |
| 4 | TTL=24h, age=1h | Unknown (→ continues) | - | - |
| 5 | `expires-at` timestamp 2h ago | Expired | 0.9+ | `expires-at-elapsed` |
| 6 | Foreign finalizer present | Blocked | 0.9+ | `foreign-finalizers` |
| 7 | Only `janitor.io/cleanup` finalizer + expired TTL | Expired | 0.9+ | `ttl-expired` |
| 8 | Protected annotation AND expired TTL | Protected | 0.9+ | `protection-annotation` |
| 9 | Live ownerRef found | Active | 0.9 | `live-owner` |
| 10 | All ownerRefs point to deleted objects | Orphaned | 0.9 | `dead-owners` |
| 11 | Pod volume mounts this ConfigMap | Active | 0.9 | `referenced` |
| 12 | No ownerRefs, no resolver references | Abandoned | 0.6 | `no-references` |
| 13 | Invalid TTL annotation (not a duration) | Error | - | - |
| 14 | Invalid expires-at annotation (not RFC3339) | Error | - | - |

### Safety constraints

- [ ] ClassProtected must prevent ANY further classification for deletion purposes.
- [ ] Confidence for ClassAbandoned must never exceed 0.7 (heuristic, not proven).
- [ ] OwnerRefStrategy must use APIReader (not cache) for owner lookup.
- [ ] FinalizerStrategy must never consider `janitor.io/cleanup` as a foreign finalizer.
- [ ] TTLStrategy must use UTC for all time comparisons.

## Observability

### Metrics

| Metric | Labels |
|--------|--------|
| `janitor_resources_classified_total` | `kind`, `class` |

### Log fields

Every Classify call logs:
- `strategy`: which strategy produced the classification
- `class`: the outcome
- `confidence`: the confidence score

## Acceptance criteria

- [ ] All 14 example scenarios have corresponding test cases
- [ ] Safety constraints have dedicated test cases
- [ ] Tests run in <5 seconds total (pure unit tests, no network)
- [ ] OwnerRefStrategy tests use fake API reader
- [ ] No test depends on real cluster access
