# Scheduled runs

`youtrack-docgen` is a one-shot command. It reads its sources, writes its output and exits, so a scheduler (cron, a CI pipeline, a systemd timer) provides the regular runs.

What a scheduled run can do today:

| Command | Effect | Exit status |
|---|---|---|
| `validate-config` | Checks `docgen.yaml` and the environment variables it references | non-zero on an invalid config |
| `generate` | Collects, filters, renders, and writes Markdown files into `sinks.markdown.dir` | non-zero if the config, a collector, the filter, or the sink fails |
| `diff` / `publish --dry-run` | Prints what `publish` would create or update in the YouTrack Knowledge Base; writes nothing | non-zero on any error |
| `publish` | Collects, filters, renders, and creates or updates the articles in the YouTrack Knowledge Base | non-zero if the config, a collector, the filter, or YouTrack fails |

> [!NOTE]
> `publish` sends nothing to YouTrack when a collector or the filter fails. If YouTrack fails in the middle of a run, the articles written so far stay; the next run continues from there.

## Principles

- **Keep secrets in the scheduler.** `docgen.yaml` references them as `${NETBOX_TOKEN}` and `${YOUTRACK_TOKEN}`. The scheduler injects the values as environment variables; never commit them.
- **Validate first.** Run `validate-config` before the real run. A missing variable fails loudly, by name, before any network call.
- **Use least-privilege tokens**: read-only for NetBox, and a YouTrack token with only the permissions the command needs.
- **Treat a non-zero exit as a failed job** and let the scheduler alert on it.
- **Review the output of a new config** with `generate` into a scratch directory before you rely on it.

## cron

[`examples/crontab`](../examples/crontab) runs a nightly generation:

```cron
15 3 * * *  /opt/youtrack-docgen/run.sh >>/var/log/youtrack-docgen.log 2>&1
```

[`examples/run.sh`](../examples/run.sh) is the wrapper the entry calls. It loads an environment file that only the service user can read, validates the config, and publishes to YouTrack. Replace `publish` with `generate` to write Markdown files instead.

## GitHub Actions

[`examples/scheduled-run.yml`](../examples/scheduled-run.yml) is a workflow you can copy into the `.github/workflows/` directory of a repository that holds your `docgen.yaml`. It runs on a schedule and on demand, installs the tool with `go install`, and takes the tokens from repository secrets. It uploads the generated Markdown as a build artifact and then publishes to YouTrack.

> [!IMPORTANT]
> The runner must be able to reach your NetBox and YouTrack instances. For internal systems use a self-hosted runner.

## Other schedulers

Any scheduler works if it can set environment variables, run the commands in order, and fail the job on a non-zero exit. Run the job as an unprivileged user, and keep the config and the output directory writable only by that user.
