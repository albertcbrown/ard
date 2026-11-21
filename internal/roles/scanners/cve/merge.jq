# merge.jq: map osv-scanner and govulncheck output to ard findings (JSONL).
#
# Inputs (all via scan, see its header):
#   --arg root <physical abs path of the scanned repo> --arg lroot <logical one>
#   --slurpfile osv <osv-scanner --format json output>     ([] or [{"results":[...]}])
#   --slurpfile gv  <JSONL, one {manifest, messages} per Go module> ([] when absent)
# Output: one compact JSON object per finding, sorted by fingerprint, keys in
# the order fingerprint,title,severity,group,files,body. Pure and deterministic:
# no timestamps, no tool versions. Written for jq 1.6+.

# osv-scanner builds absolute paths from the logical cwd ($lroot), while $root
# is the physical one; strip whichever matches.
def rel:
  (if startswith($root + "/") then .[($root | length) + 1:]
   elif startswith($lroot + "/") then .[($lroot | length) + 1:]
   else . end)
  | sub("^\\./"; "");

def excluded:
  split("/") | .[:-1] | any(. == "testdata" or . == "vendor" or . == "node_modules");

def sevrank: {"low": 1, "medium": 2, "high": 3, "critical": 4}[.] // 0;

def dbsev:
  if type != "string" then null
  else ascii_downcase
    | if . == "critical" or . == "high" or . == "low" then .
      elif . == "moderate" or . == "medium" then "medium"
      else null end
  end;

def maxsev: map(select(. != null)) | if length == 0 then null else max_by(sevrank) end;

def firstnn: map(select(. != null and . != "")) | .[0];

# ---- candidates ----

def osv_cands:
  [ $osv[] | (.results // [])[]
    | ((.source.path // "") | rel) as $m
    | (if ($m | startswith("/")) then error("cve: absolute manifest " + $m) else . end)
    | (.packages // [])[]
    | .package as $p
    | (.vulnerabilities // []) as $vs
    | (.groups // [])[] as $g
    | ($vs | map(select(.id as $i | any(($g.ids // [])[]; . == $i)))) as $mv
    | {
        tools: ["osv-scanner"],
        manifest: $m,
        package: $p.name,
        ids: (([($g.ids // [])[], ($g.aliases // [])[], ($mv[] | (.aliases // [])[])]) | unique),
        score: (($g.max_severity // null)
                | if type == "number" then .
                  elif type == "string" then (try tonumber catch null)
                  else null end),
        dbsev: ([$mv[] | .database_specific.severity | dbsev] | maxsev),
        installed: ($p.version // null),
        fixed: ([$mv[] | (.affected // [])[] | select(.package.name == $p.name)
                 | (.ranges // [])[] | (.events // [])[] | .fixed | select(. != null)] | unique),
        summary: ([$mv | sort_by(.id)[] | .summary] | firstnn),
        reach: null
      } ];

def gv_cands:
  [ $gv[] | .manifest as $m | (.messages // []) as $msgs
    | ([$msgs[] | select(.osv != null) | .osv] | map({key: .id, value: .}) | from_entries) as $osvs
    | [$msgs[] | select(.finding != null) | .finding]
    | group_by([.osv, .trace[0].module])[]
    | . as $fs
    | $fs[0] as $f
    | ($osvs[$f.osv] // {}) as $o
    | {
        tools: ["govulncheck"],
        manifest: $m,
        package: $f.trace[0].module,
        ids: (([$f.osv] + ($o.aliases // [])) | unique),
        score: null,
        dbsev: null,
        installed: ($f.trace[0].version // null | if . == null then null else ltrimstr("v") end),
        fixed: ([$fs[] | .fixed_version | select(. != null and . != "") | ltrimstr("v")] | unique),
        summary: ($o.summary // null),
        reach: (if any($fs[]; (.trace[0].function // "") != "") then "yes" else "no" end)
      } ];

# ---- merging: candidates for one manifest+package that share any id are one vuln ----

def has_osv: (.tools | index("osv-scanner")) != null;

# Joins candidates (or already-joined clusters) into one record. Commutative,
# so input order never matters.
def merge:
  . as $xs
  | {
      manifest: $xs[0].manifest,
      package: $xs[0].package,
      ids: ([$xs[].ids[]] | unique),
      fixed: ([$xs[].fixed[]] | unique),
      score: ([$xs[].score | select(. != null)] | if length == 0 then null else max end),
      dbsev: ([$xs[].dbsev] | maxsev),
      installed: (([$xs[] | select(has_osv) | .installed] | sort | firstnn)
                  // ([$xs[] | .installed] | sort | firstnn)),
      summary: (([$xs[] | select(has_osv) | .summary] | sort | firstnn)
                // ([$xs[] | .summary] | sort | firstnn)),
      tools: ([$xs[].tools[]] | unique),
      reach: ([$xs[] | .reach | select(. != null)] as $r
              | if ($r | length) == 0 then "unknown"
                elif any($r[]; . == "yes") then "yes" else "no" end)
    };

def cluster:
  reduce .[] as $c ([];
    ($c.ids) as $ids
    | (map(select(((.ids - $ids) | length) < (.ids | length)))) as $hit
    | (map(select(((.ids - $ids) | length) >= (.ids | length)))) as $rest
    | $rest + [ ($hit + [$c]) | merge ]);

# ---- rendering ----

def canonical:
  (map(select(startswith("GHSA-"))) | sort | .[0])
  // (map(select(startswith("CVE-"))) | sort | .[0])
  // (sort | .[0]);

def safeid: gsub("[^A-Za-z0-9._:-]"; "_");
def safetxt: gsub("[`\\r\\n<>]"; "");

def sev0:
  if .score != null then
    (if .score >= 9 then "critical" elif .score >= 7 then "high" elif .score >= 4 then "medium" else "low" end)
  elif .dbsev != null then .dbsev
  else "medium" end;

def render:
  (.ids | map(safeid) | canonical) as $canon
  | sev0 as $s0
  | (if .reach == "no" and ($s0 == "critical" or $s0 == "high") then "medium" else $s0 end) as $sev
  | .manifest as $m
  | .package as $pkg
  | . as $v
  | {
      fingerprint: ("osv:" + $canon + ":" + $m + ":" + $pkg),
      title: ("`" + ($pkg | gsub("[`\\r\\n]"; "")) + "`: " + $canon + " (" + $sev + ")" | .[:200]),
      severity: $sev,
      group: $m,
      files: [$m],
      body: ([
        "Installed: " + (if $v.installed == null then "unknown" else "`" + ($v.installed | safetxt) + "`" end),
        "Fixed in: " + (if ($v.fixed | length) == 0 then "none" else ($v.fixed | map("`" + safetxt + "`") | join(", ")) end),
        "Reachable: " + (if $v.reach == "no" then "no (severity capped at medium)" else $v.reach end),
        "Reported by: " + ($v.tools | join(", ")),
        "",
        "Advisory summary (untrusted text from the advisory database):",
        "```text",
        (($v.summary // "(none)") | gsub("`{3,}"; "'''") | .[:500]),
        "```",
        "",
        "Advisories:",
        ($v.ids | map(safeid) | .[]
          | "- [" + . + "](https://osv.dev/vulnerability/" + . + ")"
            + (if startswith("GHSA-") then " ([GitHub](https://github.com/advisories/" + . + "))" else "" end))
      ] | join("\n") | .[:8192])
    };

(osv_cands + gv_cands)
| map(select((.manifest | excluded) | not))
| group_by([.manifest, .package])
| map(cluster | .[])
| map(render)
| sort_by(.fingerprint)
| .[]
