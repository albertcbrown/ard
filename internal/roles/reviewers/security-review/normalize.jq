# normalize.jq: map one tool's JSON report to the aid JSONL contract.
#   --arg tool    gitleaks | gosec | semgrep
#   --arg prefix  path prefix to strip from the tool's file names
# Output: one object per result with the keys tool, rule, file, line,
# severity, message. Messages are fixed text built from the rule, never from
# matched source text: gitleaks .Match and .Secret, gosec .code and semgrep
# .extra.lines are never read.
def rel: if ($prefix != "") and startswith($prefix) then .[($prefix | length):] else . end;
def clean: gsub("[\u0000-\u001f\u007f]"; " ") | .[:300];
def sev($s): ($s // "" | ascii_downcase) as $x
  | if $x == "error" or $x == "high" then "high"
    elif $x == "warning" or $x == "medium" then "medium"
    elif $x == "critical" then "critical"
    elif $x == "low" then "low"
    else "info" end;

if $tool == "gitleaks" then
  .[]? | {tool: "gitleaks", rule: .RuleID, file: (.File | rel), line: (.StartLine // 0),
          severity: "high", message: ("Possible secret (" + ((.Description // .RuleID) | clean) + "); the match is not shown.")}
elif $tool == "gosec" then
  .Issues[]? | {tool: "gosec", rule: .rule_id, file: (.file | rel),
                line: ((.line | tostring | split("-")[0] | tonumber? ) // 0),
                severity: sev(.severity), message: (.details | clean)}
elif $tool == "semgrep" then
  .results[]? | {tool: "semgrep", rule: (.check_id | split(".") | last), file: (.path | rel), line: (.start.line // 0),
                 severity: sev(.extra.severity), message: ((.extra.message // .check_id) | clean)}
else error("unknown tool")
end
