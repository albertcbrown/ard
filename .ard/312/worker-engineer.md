---
role: engineer
status: done
attempt: 1
---
Records the #54 Kubernetes executor design (revision 3 and the owner's answers) in `docs/proposal.md`. Docs only, no code.

## Changes
- `docs/proposal.md`: Lease mentions replaced by `flock` on a `ReadWriteOncePod` volume; retries name the `evicted` cause; the Kubernetes column of the Executors table rewritten (still 3 cells per row); new "Kubernetes-executor details" paragraphs (pod shape, controller, untar rules, layout, deadlines, settings, images); Job TTL sentence; Configuration (Dockerfile bullet, `executor` line); Guardrails (egress text, isolated agent pod, result key and listener, untar boundary, cluster, residual risks); Where it runs; Open questions (decided entry plus open gaps 5 and 11).

## Testing
Checked by grep that no Lease, short-TTL or native-sidecar text remains, and that table rows keep 3 cells. `make lint test` was started but timed out after 2 minutes here, so it did not finish; the change touches only a Markdown file.

## Open issues
- The Delivery "Agent image ... never published" sentence is left as the plan says (#320 changes it); it now conflicts with the details text saying the k8s image will be public.
- The design comment link was copied from the plan's source issue text; the full design comment was not re-read here, so a reviewer should compare the wording against it.
