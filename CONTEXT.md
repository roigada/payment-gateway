# Task Management

This context models a minimal task list used to demonstrate a reusable CRUD service template.

## Language

**Task**:
A unit of work that can be tracked until completion.
_Avoid_: Item, todo

**Task ID**:
The stable identity of a **Task**. It is compared as an opaque identity, not interpreted as a meaningful format.
_Avoid_: ID string, database ID

**Title**:
The required short name of a **Task**.
_Avoid_: Name, summary

**Reopen**:
To return a completed **Task** to an incomplete state.
_Avoid_: Mark incomplete

## Relationships

- A **Task** has one completion state.
- A **Task** has exactly one **Task ID**.
- A **Task** has exactly one **Title**.
- A **Title** cannot be empty.
- A completed **Task** can be reopened.
- Deleting a **Task** removes it; deletion is not a completion state.

## Example dialogue

> **Dev:** "When a **Task** is marked complete, should it disappear from the list?"
> **Domain expert:** "No - completion changes the **Task** state; deletion is a separate action."
>
> **Dev:** "If a **Task** was completed by mistake, can it be reopened?"
> **Domain expert:** "Yes - a completed **Task** can be reopened."
>
> **Dev:** "Is a deleted **Task** still part of the task list with a deleted status?"
> **Domain expert:** "No - deleting a **Task** removes it from the task list."
>
> **Dev:** "Can we create a **Task** without a **Title** and fill it in later?"
> **Domain expert:** "No - every **Task** needs a non-empty **Title**."

## Flagged ambiguities

- "object" and "item" were used as placeholders - resolved: the canonical domain term is **Task**.
