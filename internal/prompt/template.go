// Package prompt builds the prompt text sent to the agent.
package prompt

// defaultTemplate is the Go text/template used when running in plan mode.
const defaultTemplate = `You are running inside a task loop. Each iteration starts with a fresh context. You have NO memory of previous iterations. The files below ARE your memory.

## Memory files
- PROGRESS.md — tracks which tasks are done and which remain.
- NOTES.md — working memory: decisions, gotchas, context for the next iteration.

## Workflow
1. FIRST read PROGRESS.md and NOTES.md. If either file does not exist, create it.
2. Read the plan at {{.PlanPath}}.
3. Pick the SINGLE highest-priority uncompleted task from the plan.
4. Do ONLY that one task — no scope creep.
5. Test your work.
6. Update PROGRESS.md (mark the task done, list remaining tasks) and NOTES.md (log decisions, discoveries, and context the next iteration will need). Do this BEFORE finishing.
7. Git commit ALL changes (including PROGRESS.md and NOTES.md) with a clear, descriptive commit message summarizing the task you completed. Always commit — this is your checkpoint.
8. If ALL tasks in the plan are now complete, output exactly: <promise>COMPLETE</promise>
9. Otherwise just finish normally — the loop will start a new iteration.

{{if .PlanContent}}
## Plan Content ({{.PlanPath}})
{{.PlanContent}}
{{end}}`

// defaultPrompt is returned when no plan or prompt file is provided.
const defaultPrompt = `You are running inside a task loop. Each iteration starts with a fresh context. You have NO memory of previous iterations. The files below ARE your memory.

## Memory files
- PROGRESS.md — tracks which tasks are done and which remain.
- NOTES.md — working memory: decisions, gotchas, context for the next iteration.

## Workflow
1. FIRST read PROGRESS.md and NOTES.md. If either file does not exist, create it.
2. Inspect the current project and determine one useful thing to do.
3. Do ONLY that one task — no scope creep.
4. Test your work.
5. Update PROGRESS.md (record what you did, list what remains) and NOTES.md (log decisions, discoveries, and context the next iteration will need). Do this BEFORE finishing.
6. Git commit ALL changes (including PROGRESS.md and NOTES.md) with a clear, descriptive commit message summarizing the task you completed. Always commit — this is your checkpoint.
7. If there is nothing left to do, output exactly: <promise>COMPLETE</promise>
8. Otherwise just finish normally — the loop will start a new iteration.`
