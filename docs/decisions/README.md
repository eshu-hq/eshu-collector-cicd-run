# Decisions

This folder is where we write down decisions that future readers would otherwise have to reverse-engineer from the code. We use short Architecture Decision Records (ADRs). One decision per file, numbered in the order we make them, and we don't edit old ones once they're accepted. If a later decision replaces an earlier one, the new record says so and the old one gets marked superseded.

Nothing is recorded here yet. When you make a call that a future maintainer would reasonably argue with, that's your cue to add the next record.

## How to add one

Copy the template below into `ADR-NNNN-short-title.md`, using the next free number, and add a line to the index underneath.

```markdown
# ADR-NNNN: Short title in plain words

Status: proposed | accepted | superseded by ADR-MMMM
Date: YYYY-MM-DD

## Context

What situation forced a choice? Stick to facts someone can check.

## Decision

What did we choose, in one or two sentences?

## Consequences

What gets easier, what gets harder, and what should readers watch for?
```

## Index

No ADRs yet. The first one lands here as `ADR-0001`.
