# normalize.jq: map golangci-lint output and //nolint lines to ard findings.
#
# Inputs (all via scan, see its header):
#   --arg mode      findings | baseline | fingerprints
#   --arg root      physical abs path of the linted repo; --arg lroot the logical one
#   --arg version   golangci-lint version, for the issue body and baseline header
#   --rawfile baseline  the role's baseline file (empty in baseline and fingerprints modes)
#   --slurpfile gl      JSONL, one {module, report} per Go module; report is golangci-lint's
#                       JSON ({"Issues": [...]}). [] when there is no Go module
#   --slurpfile nolint  JSONL, one {path, line, text} per //nolint line (path repo-relative)
# Output (run with -c for findings, -r for the other modes):
#   findings:     one object per finding not in the baseline, sorted by fingerprint,
#                 keys in the order fingerprint,title,severity,group,files,body
#   baseline:     the baseline file text for every current finding
#   fingerprints: every current fingerprint (before the baseline), one per line
# Pure and deterministic: no timestamps. Written for jq 1.6+. Any error(...) fails
# the run, which is closed: the controller discards it.
# Fingerprint: lint:<rule>:<path>:<msg>, see docs/proposal.md, Scanners.

def trim: sub("^\\s+"; "") | sub("\\s+$"; "");
def ws: gsub("[\u0000-\u001f\u007f]"; " ") | gsub("\\s+"; " ") | trim;
def norm: ws | gsub("\\b[0-9]+\\b"; "N");
# Untrusted text rendered inside a ```text fence: keep newlines and tabs, drop
# other control characters, neutralise fences, cap by runes.
def fence($n): gsub("[\u0000-\u0008\u000b-\u001f\u007f]"; " ") | gsub("`{3,}"; "'''") | .[:$n];
def code: gsub("`"; "'");

def rel:
  (if startswith($root + "/") then .[($root | length) + 1:]
   elif startswith($lroot + "/") then .[($lroot | length) + 1:]
   else . end)
  | sub("^\\./"; "")
  | if startswith("/") then error("lint: path outside the repository: " + .) else . end;

def checkpath:
  if test("[\u0000-\u001f\u007f]") then error("lint: path contains a control character: " + gsub("[\u0000-\u001f\u007f]"; "?"))
  elif . == "" then error("lint: issue without a file")
  else . end;

# {rule, msg}: check code ("SA4000:", "G104:") for any linter, analyzer name
# ("printf:") for govet and revive only.
def rule_and_msg($linter; $text):
  if ($text | test("^[A-Z]+[0-9]+: ")) then
    {rule: ($linter + "/" + ($text | capture("^(?<c>[A-Z]+[0-9]+): ").c)), msg: ($text | sub("^[A-Z]+[0-9]+: "; ""))}
  elif (($linter == "govet" or $linter == "revive") and ($text | test("^[a-z][a-z0-9-]*: "))) then
    {rule: ($linter + "/" + ($text | capture("^(?<c>[a-z][a-z0-9-]*): ").c)), msg: ($text | sub("^[a-z][a-z0-9-]*: "; ""))}
  else {rule: $linter, msg: $text} end;

# 512-byte fit: cut the message by runes until the fingerprint fits.
def fp($rule; $path; $msg):
  ("lint:" + $rule + ":" + $path + ":") as $base
  | if ($base | utf8bytelength) > 512 then error("lint: path too long for a fingerprint: " + $path) else . end
  | $base + ($msg | until(($base + .) | utf8bytelength <= 512; .[:-1] | sub("\\s+$"; "")));

def medium: ["bodyclose", "nilerr", "nilnesserr", "rowserrcheck", "sqlclosecheck", "durationcheck"];
def sev($rule):
  if ($rule == "govet" or ($rule | startswith("govet/")) or ($rule | startswith("staticcheck/SA"))
      or ($rule | startswith("gosec"))) then "medium"
  elif (medium | index($rule)) != null then "medium"
  else "low" end;

def dirof: if contains("/") then sub("/[^/]*$"; "") else "." end;

def title($rule; $msg; $path):
  ($rule | code) as $r
  | ($r + ": ") as $pre
  | (" (" + ($path | code) + ")") as $suf
  | (200 - ($pre | length) - ($suf | length)) as $budget
  | ($pre + $msg + $suf)
  | if length <= 200 then .
    else ($pre + ($msg | .[:([$budget - 1, 0] | max)]) + "…" + $suf)
         | if length > 200 then .[:199] + "…" else . end
    end;

def linesfmt:
  (sort | unique) as $l
  | if ($l | length) > 20
    then ($l[:20] | map(tostring) | join(", ")) + ", and " + (($l | length) - 20 | tostring) + " more"
    else ($l | map(tostring) | join(", ")) end;

def gl_cands:
  [ $gl[] | .module as $mod | (.report.Issues // [])[]
    | (.FromLinter // "") as $linter
    | (.Text // "") as $text
    | if $linter == "typecheck" then
        error("lint: " + ($mod | tostring) + ": typecheck: " + ($text | split("\n")[0] | ws))
      else . end
    | (((.Pos.Filename // "") | rel) | checkpath) as $path
    | rule_and_msg($linter; $text) as $rm
    | {
        kind: "gl", linter: $linter, rule: $rm.rule, path: $path, line: (.Pos.Line // 0),
        text: $text, tmsg: ($rm.msg | split("\n")[0] | ws),
        src: ((.SourceLines // [])[0] // null),
        fingerprint: fp($rm.rule; $path; ($rm.msg | norm))
      } ];

def nolint_cands:
  [ $nolint[]
    | (.path | checkpath) as $path
    | (.text | ws) as $t
    | { kind: "nolint", linter: "nolint", rule: "nolint", path: $path, line: .line,
        text: $t, tmsg: $t, src: null, fingerprint: fp("nolint"; $path; $t) } ];

def render:
  sort_by(.line) as $c
  | $c[0] as $f
  | ($c | map(.line)) as $lines
  | {
      fingerprint: $f.fingerprint,
      title: title($f.rule; $f.tmsg; $f.path),
      severity: sev($f.rule),
      group: ($f.rule + ":" + ($f.path | dirof)),
      files: [$f.path],
      body: (
        if $f.kind == "nolint" then
          ([ "Linter: `nolint`   Rule: `nolint`",
             "File: `" + ($f.path | code) + "`",
             "Lines: " + ($lines | linesfmt),
             "",
             "A //nolint directive hides linter findings. Fix the finding and remove the directive, or ask a human to add this fingerprint to the baseline.",
             "",
             "Directive line, first occurrence (untrusted text from the linted code):",
             "```text",
             ($f.text | fence(500)),
             "```" ] | join("\n"))
        else
          ([ "Linter: `" + ($f.linter | code) + "`   Rule: `" + ($f.rule | code) + "`",
             "File: `" + ($f.path | code) + "`",
             "Lines: " + ($lines | linesfmt),
             "Reported by: golangci-lint " + ($version | ws | code),
             "",
             "Message (untrusted text from the linted code):",
             "```text",
             ($f.text | fence(2000)),
             "```" ]
           + (if $f.src == null then []
              else [ "",
                     "Source, line " + ($f.line | tostring) + " (untrusted):",
                     "```text",
                     ($f.src | fence(500)),
                     "```" ] end)
           | join("\n"))
        end | .[:8192])
    };

def findings: (gl_cands + nolint_cands) | group_by(.fingerprint) | map(render);

def baseline_entries:
  [ $baseline | split("\n") | to_entries[]
    | (.value | sub("\r$"; "") | trim) as $l
    | select($l != "" and ($l | startswith("#") | not))
    | if ($l | startswith("lint:")) then $l
      else error("lint: baseline line " + ((.key + 1) | tostring) + " is not a fingerprint") end ];

if $mode == "fingerprints" then
  findings | map(.fingerprint) | sort | .[]
elif $mode == "baseline" then
  "# lint baseline: findings accepted for now, one fingerprint per line.",
  "# Generated by scan --baseline with golangci-lint " + ($version | ws) + ".",
  "# Commit it as <role dir>/baseline. A fingerprint listed here is never filed.",
  "# Regenerate it in the same human PR that bumps golangci-lint in the Dockerfile.",
  (findings | sort_by(.fingerprint) | .[] | ("# " + .title), .fingerprint)
else
  baseline_entries as $b
  | findings | map(select(.fingerprint as $f | ($b | index($f)) == null))
  | sort_by(.fingerprint) | .[]
end
