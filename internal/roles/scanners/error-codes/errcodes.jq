# errcodes.jq: all the logic of the error-codes scanner (see scan, its header).
# Pure and deterministic: no timestamps, no tool versions. One program, four
# modes selected with --arg mode:
#   settings  (jq -n; --slurpfile s codes.json)  validate and normalise codes.json
#   classify  (jq -R -r; --arg class r|d|s --slurpfile s settings.json)
#             file list in, the paths of that class out; a path in both
#             registry and docs is an error
#   match     (jq -R -c; --arg class r|d|s --slurpfile s settings.json)
#             "path<TAB>line<TAB>text" in, {c,p,l,code} or {skip:"code"} out
#   findings  (jq -n -c; --slurpfile m matches.jsonl --slurpfile s settings.json)
#             one finding per line, sorted by fingerprint
# Written for jq 1.6+.

def fail($m): error("codes.json: " + $m);

def esc: gsub("(?<c>[.+()\\[\\]{}|^$\\\\])"; "\\" + .c);

def globbody:
  [ match("\\*\\*|\\*|\\?|[^*?]+"; "g").string
    | if . == "**" then ".*" elif . == "*" then "[^/]*" elif . == "?" then "[^/]" else esc end ]
  | join("");

# "dir/" is a prefix from the root; no "/" matches the base name; anything else
# is a glob on the whole path ("*" and "?" stop at "/", "**" does not).
def g2re:
  if endswith("/") then "^" + esc + ".*"
  elif contains("/") then "^" + globbody + "$"
  else "^(?:.*/)?" + globbody + "$" end;

def compiles($what): . as $re | try ("" | test($re)) catch fail($what + " does not compile");

def strlist($k):
  if type != "array" then fail($k + " must be a list of strings")
  elif length > 100 then fail($k + " has more than 100 entries")
  elif any(.[]; (type != "string") or . == "" or length > 512) then fail($k + " must hold non-empty strings of at most 512 characters")
  else map(g2re) end;

def settings:
  $ARGS.named.s[0] as $c
  | if ($c | type) != "object" then fail("must be a JSON object") else . end
  | ($c | keys - ["pattern", "registry", "entry", "docs", "sources", "exclude"]) as $u
  | if ($u | length) > 0 then fail("unknown key " + ($u | join(", "))) else . end
  | if ($c.pattern | type) != "string" or $c.pattern == "" or ($c.pattern | length) > 512
    then fail("pattern must be a non-empty string of at most 512 characters") else . end
  | ("(?<![A-Za-z0-9_])(?:" + $c.pattern + ")(?![A-Za-z0-9_])") as $re
  | ($re | compiles("pattern"))
  | ("^(?:" + $c.pattern + ")$" | compiles("pattern")) as $_
  | if ("" | test("^(?:" + $c.pattern + ")$")) then fail("pattern matches the empty string") else . end
  | if ($c.registry | type) != "array" or ($c.registry | length) == 0 then fail("registry must be a non-empty list of path patterns") else . end
  | if ($c | has("entry")) and (($c.entry | type) != "string" or $c.entry == "" or ($c.entry | length) > 512) then fail("entry must be a non-empty string of at most 512 characters") else . end
  | ($c.entry // null) as $entry
  | (if $entry != null then ($entry | compiles("entry")) else null end) as $_
  | {
      pattern: $c.pattern,
      re: $re,
      entry: $entry,
      registry: ($c.registry | strlist("registry")),
      docs: (($c.docs // []) | strlist("docs")),
      sources: (if $c | has("sources") then ($c.sources | strlist("sources")) else null end),
      exclude: (($c.exclude // []) | strlist("exclude"))
    };

def anyre($l): . as $p | any($l[]; . as $r | $p | test($r));

def classify:
  . as $line
  | ltrimstr("./") as $p
  | $ARGS.named.s[0] as $c
  | ($p | anyre($c.registry)) as $r
  | ($p | anyre($c.docs)) as $d
  | if $r and $d then error("file matches both registry and docs: " + $p)
    elif ($ARGS.named.class == "r" and $r) or ($ARGS.named.class == "d" and $d) then $line
    elif $ARGS.named.class == "s" and ($r | not) and ($d | not)
         and ($c.sources == null or ($p | anyre($c.sources)))
         and (($p | anyre($c.exclude)) | not) then $line
    else empty end;

def matchline:
  $ARGS.named.s[0] as $c
  | (capture("^(?<p>[^\t]*)\t(?<l>[0-9]+)\t(?<t>.*)$") // error("malformed line from awk")) as $ln
  | select($ARGS.named.class != "r" or $c.entry == null or ($ln.t | test($c.entry)))
  | ($ln.t | [match($c.re; "g").string | select(length > 0)] | unique)[] as $code
  | if $code | test("^[A-Za-z0-9_.:/+=-]{1,128}$")
    then {c: $ARGS.named.class, p: ($ln.p | ltrimstr("./")), l: ($ln.l | tonumber), code: $code}
    else {skip: "code"} end;

def safe: gsub("[`\r\n<>]"; "");
def code: "`" + gsub("`"; "'") + "`";

def locs:
  length as $n
  | ((.[:50] | map("\(.p | safe | gsub("@"; "＠")):\(.l)") | join("\n"))
     + (if $n > 50 then "\n… and \($n - 50) more" else "" end));

def mk($kind; $sev; $grp; $title; $sentence):
  {
    fingerprint: "error-codes:\($kind):\(.key)",
    title: ((.key | safe | code) + " " + $title),
    severity: $sev,
    group: $grp,
    files: (.value | map(.p) | unique | .[:20]),
    body: ($sentence + "\n\nLocations:\n\n```text\n" + (.value | locs) + "\n```\n")
  };

def bycode($c): [$ARGS.named.m[] | select(.c == $c)] | group_by(.code)
  | map({key: .[0].code, value: (map({p, l}) | unique)});

def findings:
  $ARGS.named.s[0] as $c
  | (bycode("r") | from_entries) as $R
  | (bycode("s")) as $S
  | ([$ARGS.named.m[] | select(.c == "d") | .code] | unique) as $D
  | [ ($R | to_entries[] | select(.value | length >= 2)
       | mk("duplicated"; "high"; "duplicate codes"; "is defined more than once in the registry";
            "This code is defined on two or more registry lines. Give each definition its own code.")),
      ($S[] | select(.key as $k | $R | has($k) | not)
       | mk("unregistered"; "medium"; "unregistered codes"; "is used but not in the registry";
            "This code appears in the repository but no registry file defines it. Add it to the registry or remove the use.")),
      (if ($c.docs | length) > 0
       then ($R | to_entries[] | select(.key as $k | $D | index($k) | not)
             | mk("undocumented"; "low"; "undocumented codes"; "is in the registry but not documented";
                  "This code is defined in the registry but no docs file mentions it. Document it."))
       else empty end) ]
  | sort_by(.fingerprint)
  | if length > 5000 then error("more than 5000 findings: the pattern is too broad") else . end
  | .[];

if $mode == "settings" then settings
elif $mode == "classify" then classify
elif $mode == "match" then matchline
elif $mode == "findings" then findings
else error("unknown mode " + $mode) end
