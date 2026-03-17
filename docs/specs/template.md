# Feature Spec: [Feature Name]

**Status**: Draft | Review | Accepted | Implemented
**Author**: @author
**Date**: YYYY-MM-DD
**Phase**: 0 | 1 | 2 | 3 | 4

---

## Summary

One paragraph describing what this feature does and why it matters.

## Motivation

What problem does this solve? What happens without it?

## Behavioral Specification

### Core behavior

Describe the expected behavior in "Given / When / Then" or prose form.

### Examples

List concrete examples that will map directly to test cases:

| # | Input / Situation | Expected Output | Confidence |
|---|---|---|---|
| 1 | ... | ... | High |
| 2 | ... | ... | Medium |

### Edge cases

- What happens when ...?
- What should NOT happen when ...?

### Safety constraints

If this feature could affect deletion or resource mutation, list explicit safety requirements:
- [ ] Dry-run mode must not affect ...
- [ ] Protected resources must never ...
- [ ] Confidence below X must not trigger ...

## API changes

### New CRD fields (if any)

```yaml
# Before
spec:
  existing: field

# After
spec:
  existing: field
  newField:  # description
    type: string
```

### New annotations/labels

| Key | Values | Description |
|-----|--------|-------------|
| `janitor.io/...` | ... | ... |

## Implementation plan

1. Write failing tests (see Testing section)
2. Implement `internal/<package>/<file>.go`
3. Wire into reconciler
4. Verify tests pass
5. Update CLAUDE.md if architecture changed

## Testing

### Unit test cases

Map to examples above:
- `Test<Feature>_<ExampleDescription>`
- `Test<Feature>_<EdgeCase>`
- `Test<Feature>_Safety_<Constraint>`

### Integration test cases (if needed)

- Test against envtest with real API server
- Verify reconciler produces expected Events

### Fixtures needed

- What fake objects or test data are needed?

## Observability

### New metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `janitor_...` | Counter | `kind`, ... | ... |

### Log fields

Any new structured log fields that should be present?

### Events

| Reason | Type | When |
|--------|------|------|
| `...` | Normal/Warning | ... |

## Risks and mitigations

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| ... | Low/Med/High | Low/Med/High | ... |

## Open questions

- [ ] Unresolved design question 1
- [ ] Unresolved design question 2

## Acceptance criteria

The feature is complete when:
- [ ] All test cases pass
- [ ] CI is green
- [ ] Events are emitted for all significant outcomes
- [ ] Metrics are recorded
- [ ] Dry-run behavior is verified
- [ ] Protection behavior is verified
- [ ] Documentation updated
