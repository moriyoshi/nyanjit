#!/bin/sh
# usage: classify.sh pre.txt post.txt -> counts per class; writes pre/post mismatch keys to stdout tail
awk '
function key(l,  a){split(l,a," "); return a[1]" "a[2]" "a[3]" "a[4]}
function st(l,  a){split(l,a," "); return a[5]}
function rest(l,  i,a,s){split(l,a," "); s=""; for(i=6;i<=length(a);i++) s=s" "a[i]; return s}
NR==FNR { pre[key($0)]=$0; next }
{ k=key($0); total++
  if (!(k in pre)) { onlypost++; print "ONLYPOST " $0 > "/dev/stderr"; next }
  p=pre[k]; delete pre[k]
  sp=st(p); sq=st($0)
  if (p==$0) { if (sq=="OK") ok++; else if (sq=="ERR") err++; else other++; next }
  if (sp=="OK" && sq=="OK") { lst++; print "LISTING " p " || " $0 > "/dev/stderr"; next }
  if (sp!=sq) { flip++; print "FLIP " p " || " $0 > "/dev/stderr"; next }
  pn=p; qn=$0; gsub(/ \(pins[^)]*\)/,"",pn); gsub(/ \(pins[^)]*\)/,"",qn)
  if (pn==qn) { errpins++; next }
  errtext++; print "ERRTEXT " p " || " $0 > "/dev/stderr"
}
END { for (k in pre) onlypre++
  printf "total=%d identical_ok=%d identical_err=%d identical_other=%d err_pins_only=%d err_text_other=%d listing_diff=%d flip=%d onlypre=%d onlypost=%d\n", total, ok, err, other, errpins, errtext, lst, flip, onlypre, onlypost }
' "$1" "$2"
