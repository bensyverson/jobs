IMPORTANT: As you're implementing features, you MUST keep the [Documentation](docs/content/docs/) up to date, updating [README](README.md) only if a new documentation file has been added, or if new users NEED to know about your changes.

## Overview

You are working on Jobs, a hierarchical task manager for the CLI, backed by an event store in SQLite.

## Documentation

When you need additional context, consult the docs:

- [README.md](README.md) - Project overview and quick start
- [project/](project/) - Historical design documents and past project plans

## CLI conventions

- **Open by running `job orient` (standalone tool, no arguments).** At session start, `job orient` is the landscape briefing.

## General

- If a requirement is ambiguous or could be solved in several ways, choose the most idiomatic way to solve the problem in the given language if that would resolve the ambiguity.
- If the ambiguity is more about the requirement itself, or you face an architectural question, don't make a decision—stop and ask the user instead.
- Avoid dependencies unless the required functionality would be unreasonable to re-implement. If you MUST bring in a dependency, get the user's permission first.
- IMPORTANT: In this project we ALWAYS follow strict "red/green" TDD; write tests for all example cases we need to handle and any new methods we're implementing, verify that they fail, and *then* proceed to implement your code changes. If you must alter a previous test to get it to pass, explain exactly WHY to the user and get their consent.
- Before fixing a bug, try to create a regression test to catch it in the future.
- DO NOT begin a new chat by doing an extensive exploration of the entire codebase. That is wasteful, as this is a large codebase. Instead, read the README and use an Explore agent to read the DocC documentation if you want to get the lay of the land. Of course, once you have a specific need, you can explore as much of the code as you require.
- To create and manage plans and task lists, always use the `job` command. Do not use Plan Mode.

## Understand the "why"
Before you answer a question or respond to a request, you must understand **why** the user has made this request. Beware of the "XY Problem." If the user's motivation or goal is not 100% clear, first ask clarifying questions until you fully understand what they're trying to achieve.

## Diverge, then converge
Once you understand the prompt, rather than jumping to a solution, you will use divergent thinking. Brainstorm other options. Weigh these options against the user's preferences and overall objectives. Then, converge on a recommendation, and ask for confirmation from the user. Only then can you fully converge and begin executing on a direction.

## Analysis
It will sometimes be valuable to create a script or tool in order to aid your analysis. Before you do, check the `scripts` directory to make sure it doesn't already exist. If it doesn't, rather than creating a disposable or inline script, add it to the `scripts` directory, so the user can re-run the tool in the future.

## Pre-completion Critique
Before you declare a task done, or a question answered, pause and critique your own work. Return to the context of the original user request—have you truly addressed their need? If you're producing code, do all linting and unit tests pass? If you're synthesizing or analyzing information, what are the gaps or weaknesses in your answer? What would a relevant and intelligent expert say about your work? If you identify serious flaws, keep working until you resolve them.

## Tidiness
Don't create temporary files in the root of the directory and leave them there. If you truly need a transient file, that's fine, but delete it when you're done. But if the artifact is something valuable (such as an agent's report, or a script), please save it in the correct directory.

## Git workflow
At moments when significant work has been completed and accepted by the user, offer to commit the changes for them. You may need to pull changes and resolve conflicts, which you should do for the user using git rebase whenever possible. If there is a real conflict, ask the user how they want to resolve it, explaining the situation clearly. Always commit all uncommitted files together, as your changes may depend on prior changes, and we don't want to commit the code in a half-working state. Do not amend previous commits.

## Development workflow

- All Git commit messages should complete the sentence "This commit...", e.g. "Adds an email verification flow". You can then go on to detail the change with bullets or headers in the body of the commit.
- IMPORTANT: In this project we ALWAYS follow strict "red/green" TDD; write tests for all example cases we need to handle and any new methods we're implementing, verify that they *all* fail, and *then* proceed to implement your code changes. If a new test is green during the red stage, it's testing nothing and should be removed or refactored.
- If, during development, you must update an existing test to get it to pass, explain exactly WHY to the user and get their consent.
- Before fixing any bug, try to create a regression test to catch it in the future.
- DO NOT begin by doing an extensive exploration of the entire codebase. That is wasteful, as this is a large codebase. Instead, read the README and use an Explore agent to read the documentation if you want to get the lay of the land. Of course, once you have a specific need, you can explore as much of the code as you require.

## Development Stage

- We are in BUILD mode. This library is pre-launch, with zero users or existing files. When refactoring or implementing new features, we NEVER need to consider backward compatibility. We can assume that every use of the project is green-field. We're trying to architect a clean, clear API and codebase without any baggage. With that said, make sure the user knows when you make breaking changes, and be sure to update any relevant unit tests.
- In build mode, we are ambitious. Even if you think a feature will take weeks or months, if it's important, let's take it on now. We will build what we know is needed to achieve the overall project goals, without taking shortcuts or implementing the MVP. But we will balance this with avoiding "future-proofing" or over-engineering.

## Database Migrations

- Whenever you need to make a change to the database schema, you must create a migration in the [migrations](./internal/migrations/) folder, with an incremented leading number.
- DO NOT run this migration manually. There is an automatic migration system that runs when the server starts up, and it records the current migration number in the database. So instead of manually migrating the database, just start or restart the server.

## Golang-specific

- Prior to commiting changes, you MUST run `go fix` and `gofmt` to correctly format your code, and you must run the tests related to any features you added or changed. `go fix`, `gofmt` and unit tests with all be run as pre-commit hooks, so this will allow you to catch problems before attempting a commit.
