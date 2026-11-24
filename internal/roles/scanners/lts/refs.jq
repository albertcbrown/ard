# refs.jq: manifest records in, version references out.
#
# Run:  jq -c -n --slurpfile in inputs.jsonl --slurpfile map products.json -f refs.jq
# Input records: {kind: gomod|packagejson|dockerfile, path, text} or
# {kind: helmvalues, path, json}. Output lines: {product, version, path, line,
# ref, floor}, sorted, then {skip: "..."} lines. Pure; valid on jq 1.6.
# A package.json that is not JSON fails the whole run (fail closed).

$map[0] as $m
| def lines: split("\n") | to_entries | map({n: (.key + 1), s: .value});
  def lead: capture("^(?<v>[0-9]+(\\.[0-9]+)*)").v;
  def vkey: split(".") | map(tonumber);
  def isstr: type == "string";
  # Lowest lower bound of an npm-style range, or null.
  def lowest:
    [ split("||")[] | split(" ")[] | select(length > 0)
      | select(startswith("<") | not)
      | sub("^[=>~^v]+"; "") | select(test("^[0-9]")) | lead ]
    | if length == 0 then null else min_by(vkey) end;
  def lineof($text; $needle):
    ($text | lines | map(select(.s | contains($needle))) | .[0].n) // 1;
  def image_ref($image; $tag; $path; $line; $ref):
    ($image | sub("@sha256:.*$"; "") | sub("^(index\\.)?docker\\.io/"; "") | sub("^library/"; "")) as $name
    | if $m.images[$name] == null then {skip: ("unmapped image " + $name + " (" + $path + ")")}
      elif $tag == null or $tag == "" or $tag == "latest" or $tag == "lts" then
        {skip: ("unpinned tag for " + $name + " (" + $path + ":" + ($line | tostring) + ")")}
      else
        (($tag | tostring) | (try lead catch null) // (split("-")[0] | ascii_downcase)) as $v
        | {product: $m.images[$name], version: $v, path: $path, line: $line, ref: $ref, floor: false}
      end;
  def split_image:
    sub("@sha256:.*$"; "")
    | (capture("^(?<i>[^:]*(:[0-9]+/[^:]*)?):(?<t>[^:/]+)$") // {i: ., t: null});

  [ $in[]
    | .path as $path
    | if .kind == "gomod" then
        (.text | lines) as $ls
        | ($ls | map(select(.s | test("^toolchain[ \t]+go[0-9]")))) as $tc
        | if ($tc | length) > 0 then
            $tc[0] | {product: "go", version: (.s | sub("^toolchain[ \t]+go"; "") | lead), path: $path, line: .n,
                      ref: .s, floor: false}
          else
            ($ls | map(select(.s | test("^go[ \t]+[0-9]"))))[0]
            | select(. != null)
            | {product: "go", version: (.s | sub("^go[ \t]+"; "") | lead), path: $path, line: .n, ref: .s, floor: true}
          end
      elif .kind == "packagejson" then
        .text as $text
        | (try ($text | fromjson) catch error("lts: cannot parse " + $path)) as $j
        | if ($j | type) != "object" then empty else
            ( ($j.engines.node? // null) as $r
              | if ($r | isstr) then
                  ($r | lowest) as $v
                  | if $v == null then {skip: ("engines.node range not understood in " + $path)}
                    else {product: "nodejs", version: $v, path: $path, line: lineof($text; "\"node\""),
                          ref: ("engines.node " + $r), floor: true} end
                else empty end),
            ( ["dependencies", "devDependencies"][] as $sec
              | ($j[$sec] // {}) | if type == "object" then to_entries[] else empty end
              | select($m.npm[.key] != null and (.value | isstr))
              | .key as $k | .value as $r
              | ($r | lowest) as $v
              | if $v == null then {skip: ("npm range for " + $k + " not understood in " + $path)}
                else {product: $m.npm[$k], version: $v, path: $path, line: lineof($text; "\"" + $k + "\""),
                      ref: ($k + " " + $r), floor: true} end )
          end
      elif .kind == "dockerfile" then
        (.text | lines) as $ls
        | ($ls | map(select(.s | test("^\\s*from\\s"; "i"))) | .[0].n // 1000000000) as $first
        | (reduce ($ls[] | select(.n < $first and (.s | test("^\\s*arg\\s+[A-Za-z_][A-Za-z0-9_]*="; "i")))) as $l ({};
              ($l.s | sub("^\\s*[Aa][Rr][Gg]\\s+"; "") | capture("^(?<k>[A-Za-z_][A-Za-z0-9_]*)=(?<v>.*)$")) as $kv
              | .[$kv.k] = ($kv.v | sub("\\s+$"; "") | sub("^\"(?<x>.*)\"$"; .x) | sub("^'(?<x>.*)'$"; .x)))) as $args
        | reduce ($ls[] | select(.s | test("^\\s*from\\s"; "i"))) as $l ({stages: [], out: []};
            ($l.s | split(" ") | map(select(length > 0))) as $t
            | ($t[1:] | map(select(startswith("--") | not))) as $rest
            | ($rest[0] // "") as $raw
            | (reduce ($args | keys[]) as $k ($raw;
                gsub("\\$\\{" + $k + "(:-[^}]*)?\\}"; $args[$k])
                | gsub("\\$" + $k + "(?![A-Za-z0-9_])"; $args[$k]))
              | gsub("\\$\\{[A-Za-z_][A-Za-z0-9_]*:-(?<d>[^}]*)\\}"; .d)) as $img
            | (if ($rest[1] // "" | ascii_downcase) == "as" then ($rest[2] // "" | ascii_downcase) else null end) as $stage
            | .out += [ if $img == "" or ($img | contains("$")) then
                          {skip: ("unresolved image \"" + $raw + "\" (" + $path + ":" + ($l.n | tostring) + ")")}
                        elif $img == "scratch" or (.stages | index($img | ascii_downcase)) != null then empty
                        else
                          ($img | split_image) as $si
                          | image_ref($si.i; $si.t; $path; $l.n; ($l.s | sub("^\\s+"; "") | sub("\\s+$"; "")))
                        end ]
            | if $stage != null then .stages += [$stage] else . end)
        | .out[]
      elif .kind == "helmvalues" then
        ( .json | [.. | objects] | .[]
          | (select((.repository | isstr) and ((.tag | type) == "string" or (.tag | type) == "number"))
              | image_ref(.repository; (.tag | tostring); $path; 0; "repository " + .repository + " tag " + (.tag | tostring))),
            ( to_entries[] | select(.key == "image" and (.value | isstr) and (.value | contains(":")))
              | .value as $v | ($v | split_image) as $si
              | image_ref($si.i; $si.t; $path; 0; "image " + $v) ) )
      else empty end ]
| ( map(select(.skip == null)) | unique_by([.product, .version, .path, .line, .ref]) | sort_by([.product, .version, .path, .line]) ) as $refs
| ( map(select(.skip != null)) | unique_by(.skip) ) as $skips
| ($refs[], $skips[])
