# eol.jq: version references plus endoflife.date data in, findings out.
#
# Run:  jq -c -n --arg today YYYY-MM-DD --slurpfile refs refs.jsonl \
#         --slurpfile api api.json -f eol.jq
# $api is one JSON array of v1 API responses ({result: {name, label,
# releases[]}}). One finding per product and release cycle, sorted by
# fingerprint, then {skip: "..."} lines. Pure; valid on jq 1.6.
# Windows (days to end of life): <= 0 high, <= 90 medium, <= 180 low.
# A component whose references are all floors (lowest supported version, not
# what runs) is capped at medium. Untrusted text is cleaned and shown in code
# spans only.

def clean: gsub("[\\x00-\\x1f\\x7f]"; " ") | gsub("`"; "");
def days($d): (($d + "T00:00:00Z" | fromdate) - ($today + "T00:00:00Z" | fromdate)) / 86400;
def sevrank: if . == "high" then 3 elif . == "medium" then 2 else 1 end;

($api[0] // []) as $apis
| ($apis | map({key: .result.name, value: .result}) | from_entries) as $by
| ($refs | map(select(.skip == null))) as $rs
| ( $rs | map(.product) | unique | map(
      if $by[.] == null or (($by[.].releases | type) != "array") then error("lts: no API data for product " + .) else empty end) ) as $chk
| def match($rel; $v):
    ($v | ascii_downcase) as $lv
    | ([$rel[] | select((.codename // "") != "" and ((.codename | split(" ")[0] | ascii_downcase) == $lv))] | .[0]) as $byname
    | if $byname != null then $byname else
        ($v | split(".")) as $p
        | first(range($p | length; 0; -1) as $n | ($p[0:$n] | join(".")) as $c | ($rel[] | select(.name == $c)) , empty) // null
      end;
  [ $rs[]
    | . as $r
    | ($by[$r.product].releases) as $rel
    | (match($rel; $r.version)) as $rl
    | if $rl == null then {skip: ("no " + $r.product + " cycle for " + $r.version + " (" + $r.path + ":" + ($r.line | tostring) + ")")}
      else {r: $r, rl: $rl} end ] as $m
| ($m | map(select(.skip != null))) as $skips
| ( $m | map(select(.skip == null)) | group_by([.r.product, .rl.name])
    | map(
        .[0].rl as $rl | .[0].r.product as $p | $by[$p] as $api1
        | (.[0].rl.name) as $cycle
        | ([.[].r] | sort_by([.path, .line, .ref])) as $refs1
        | (if $rl.eolFrom != null then days($rl.eolFrom) else null end) as $d
        | (if $d != null then (if $d <= 0 then "high" elif $d <= 90 then "medium" elif $d <= 180 then "low" else null end)
           elif $rl.isEol == true then "high" else null end) as $sev0
        | select($sev0 != null)
        | (if $sev0 == "high" and ($refs1 | all(.floor)) then "medium" else $sev0 end) as $sev
        | ($api1.label | clean) as $label
        | ($rl.eolFrom // "no date") as $eol
        | ($api1.releases | map(select(.isMaintained == true))) as $maint
        | (($maint | map(select(.isLts == true)) | .[0]) // $maint[0]) as $newest
        | ($refs1 | length) as $n
        | { fingerprint: ("eol:" + $p + ":" + $cycle),
            title: ($label + " " + ($cycle | clean) + ": end of life " + (if $rl.eolFrom != null then $eol else "(no date)" end)),
            severity: $sev,
            group: $p,
            files: ($refs1 | map(.path) | unique),
            body: ( "End of life: " + $eol + " (endoflife.date: " + $label + " " + ($cycle | clean) + ")\n"
                  + (if $rl.latest.name != null then "Latest " + ($cycle | clean) + " release: " + ($rl.latest.name | tostring | clean) + "\n" else "" end)
                  + (if $newest != null then "Newest maintained cycle: " + ($newest.name | clean) + (if $newest.latest.name != null then " (latest " + ($newest.latest.name | tostring | clean) + ")" else "" end) + "\n" else "" end)
                  + "Details: https://endoflife.date/" + $p + "\n\n"
                  + "References (from repository files; untrusted text):\n"
                  + ( $refs1[0:50] | map("- `" + ((.path | clean) + (if .line > 0 then ":" + (.line | tostring) else "" end)) + "`: `" + (.ref | clean | .[0:200]) + "`") | join("\n") )
                  + (if $n > 50 then "\n…and " + (($n - 50) | tostring) + " more" else "" end) ) } )
    | sort_by(.fingerprint) ) as $out
| $out[], ($skips | map({skip: .skip}) | unique_by(.skip)[])
