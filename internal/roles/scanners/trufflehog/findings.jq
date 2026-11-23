# project: trufflehog --json result lines in; one "<base64 secret>\t<json>" line out
#          per result. The json holds only d (DetectorName), f (file), l (line).
#          Errors carry fixed text only (scan drops this stage's stderr anyway).
# merge:   "<h16>\t<json>" lines in (-n -R, inputs); findings out.
def die(m): error("trufflehog: " + m);

def project:
  if type != "object" then die("result is not an object") else . end
  | (.SourceMetadata.Data.Filesystem // die("result has no filesystem location")) as $fs
  | (if (.Raw | type) == "string" and .Raw != "" then .Raw
     elif (.RawV2 | type) == "string" and .RawV2 != "" then .RawV2
     else die("result has no Raw or RawV2 value") end) as $secret
  | (.DetectorName | if type == "string" and test("^[A-Za-z0-9_.-]{1,64}$") then . else die("bad detector name") end) as $d
  | ($fs.file | if type == "string" then ltrimstr("./") else die("result file is not a string") end) as $f
  | if $f == "" or ($f | startswith("/")) or ($f | split("/") | any(. == ".." or . == "." or . == ""))
       or ($f | explode | any(. < 32 or . == 127))
    then die("result file is not a clean repo-relative path") else . end
  | ($fs.line // 0 | if type == "number" and . >= 0 then floor else die("bad line") end) as $l
  | "\($secret | @base64)\t\({d: $d, f: $f, l: $l} | tojson)";

# A code span whose fence is longer than any backtick run in the text.
def code: ([match("`+"; "g").length] | max // 0) as $n | ("`" * ($n + 1)) as $fence
  | if $n > 0 then "\($fence) \(.) \($fence)" else "\($fence)\(.)\($fence)" end;

def merge($allow):
  [inputs | select(length > 0) | split("\t")
   | if length != 2 or (.[0] | test("^[0-9a-f]{16}$") | not) then die("bad hashed line") else . end
   | {h: .[0]} + (.[1] | fromjson)]
  | map(select(("trufflehog:" + .h) as $fp | $allow | index($fp) | not))
  | group_by(.h)
  | map(("trufflehog:" + .[0].h) as $fp
      | (map(.d) | unique) as $dets
      | (map({f, l}) | unique_by([.f, .l]) | sort_by(.f, .l)) as $locs
      | ($locs | map(.f) | unique) as $files
      | {fingerprint: $fp,
         title: ("Possible \($dets[0]) credential in \($files[0])" | .[0:200]),
         severity: "high", group: "", files: $files[0:1000],
         body: ([
           "trufflehog matched a value that looks like a credential (detector: \($dets | map(code) | join(", "))). ard did not verify it and never stores or prints the value.",
           "",
           "**Treat it as leaked.** Revoke or rotate it at the provider first. Deleting the line does not revoke it: the value stays in git history, and on a public repository it is already public.",
           "",
           "**Do not add `ard:pending` to this issue before the credential is rotated.** Triage and worker agents would read the file that holds it.",
           "",
           "What to do: 1. Rotate or revoke the credential at the provider. 2. Remove the value from HEAD in a PR. The issue closes itself on the next run (within 24 hours) once the value is gone from HEAD; that means gone from HEAD, not rotated. 3. Do not close this issue as completed while the value is still at HEAD: the next run refiles it. 4. For a false positive or a test fixture, add the fingerprint to `allow` or the path to `exclude-paths` (a reviewed PR), or close as not planned (it refiles after 90 days). 5. At 5 open issues the scanner does not run, so close fixed ones by hand once the value is gone. 6. Git history is not scanned or rewritten; rotation is what makes that safe.",
           "",
           "Not a secret? A maintainer adds the fingerprint below to `.ard/scanners/trufflehog/allow`, or a path pattern to `.ard/scanners/trufflehog/exclude-paths`, in a reviewed PR. Inline `trufflehog:ignore` comments are not honoured.",
           "",
           "Fingerprint: `\($fp)`",
           "",
           "Locations at the scanned commit (\($locs | length)):"]
           + ($locs[0:50] | map("- \(.f | code)" + (if .l > 0 then " line \(.l)" else "" end)))
           + (if ($locs | length) > 50 then ["- and \(($locs | length) - 50) more"] else [] end)
           + (if ($files | length) > 1000 then ["", "Only the first 1,000 of \($files | length) files are listed."] else [] end)
           | join("\n"))})
  | sort_by(.fingerprint) | .[];

if $stage == "project" then project
elif $stage == "merge" then merge($allow | split("\n") | map(sub("#.*$"; "") | gsub("^\\s+|\\s+$"; "")) | map(select(. != "")))
else die("unknown stage") end
