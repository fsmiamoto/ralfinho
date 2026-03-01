# Notes

- CLI parsing now rejects positional args when --prompt/--plan are provided and errors on multiple prompt positionals; tests cover these cases.
- Both `defaultTemplate` (plan mode) and `defaultPrompt` (default/no-plan mode) in `internal/prompt/template.go` now include a git commit step (step 7 in plan mode, step 6 in default mode) instructing the model to commit all changes after updating PROGRESS.md and NOTES.md.
- Tests in `prompt_test.go` verify the "Git commit" instruction is present in both `BuildFromPlan` and `BuildDefault` outputs.
- Ran go test ./...; all tests pass.
