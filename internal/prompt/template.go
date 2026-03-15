// Package prompt builds the prompt text sent to the agent.
package prompt

// defaultTemplate is the Go text/template used when running in plan mode.
const defaultTemplate = `You are running inside a task loop. Each iteration starts with a fresh context. You have NO memory of previous iterations. The files below ARE your memory.

## Memory files
- PROGRESS.md — tracks which tasks are done and which remain.
- NOTES.md — working memory: decisions, gotchas, context for the next iteration.

## Workflow
1. FIRST read PROGRESS.md and NOTES.md. If either file does not exist, create it.
2. Study the plan at {{.PlanPath}} carefully.
3. Pick the SINGLE highest-priority uncompleted task from the plan.
4. Do ONLY that one task — no scope creep.
5. Test your work the best you can.
  - Unit tests are important, but we should actually try running the code and seeing the results like an engineer would at each step.
6. Update PROGRESS.md (mark the task done, list remaining tasks) and NOTES.md (log decisions, discoveries, and context the next iteration will need). Only include things you find really relevant. Do this BEFORE finishing.
7. Git commit ONLY the files related to the task you completed with a clear, descriptive commit message. Do NOT include PROGRESS.md or NOTES.md in this commit — those are loop-internal memory files, not project artifacts. Also don't include any Co-Authored by in the message.
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
2. Study the current project and determine one useful thing to do.
   - It could be including new unit tests for uncovered code.
   - Adding a good linter and fixing some recommendations
   - Rewriting a piece of the code hard to understand.
   - Refactor the code design to follow architecture best practices
3. After you pick a task, ONLY do that one task — no scope creep.
4. Test your work the best you can.
  - Unit tests are important, but we should actually try running the code and seeing the results like an engineer would at each step.
5. Update PROGRESS.md (record what you did, list what remains) and NOTES.md (log decisions, discoveries, and context the next iteration will need). Do this BEFORE finishing.
  - If you think there's something important to be done but you didn't do, include it in the NOTES.
6. Git commit ONLY the files related to the task you completed with a clear, descriptive commit message. Do NOT include PROGRESS.md or NOTES.md in this commit — those are loop-internal memory files, not project artifacts. Also don't include any Co-Authored by
7. If there is nothing left to do, output exactly: <promise>COMPLETE</promise>
8. Otherwise just finish normally — the loop will start a new iteration.`
