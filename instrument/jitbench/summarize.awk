# summarize.awk ENGINE.txt... — merges harness outputs (nyanjit.txt,
# sljit.txt, tpde.txt, cranelift.txt) into Markdown tables. Compile is IR
# construction through machine code, without mapping it executable: the
# nyanjit compile column, sljit total, and build + compile for TPDE and
# Cranelift. Run is the best call time.
function us(v,  n) {
	n = v + 0
	if (v ~ /ns$/) return n / 1000
	if (v ~ /(µs|us)$/) return n
	if (v ~ /ms$/) return n * 1000
	if (v ~ /s$/) return n * 1000000
	return n
}
function fmt(x) {
	if (x == "") return "–"
	if (x >= 100) return sprintf("%.0f", x)
	if (x >= 10) return sprintf("%.1f", x)
	if (x >= 1) return sprintf("%.2f", x)
	return sprintf("%.3g", x)
}
FNR == 1 { engine = FILENAME; sub(/.*\//, "", engine); sub(/\.txt$/, "", engine) }
$1 == "kernel" || NF < 6 { next }
engine == "nyanjit"   { col = "nyanjit " $2; comp[$1, col] = us($3); run[$1, col] = us($7); seen[col] = 1 }
engine == "sljit"     { col = "sljit"; comp[$1, col] = us($3); run[$1, col] = us($6); seen[col] = 1 }
engine == "tpde"      { col = "TPDE " $2; comp[$1, col] = us($3) + us($4); run[$1, col] = us($9); seen[col] = 1 }
engine == "cranelift" { col = "Cranelift " $2; comp[$1, col] = us($3) + us($4); run[$1, col] = us($9); seen[col] = 1 }
END {
	nk = split("tiny chain100 chain1000 loop pressure collatz fp", ks, " ")
	nc = split("nyanjit baseline|nyanjit optimized|sljit|TPDE ssa|TPDE alloca|Cranelift speed|Cranelift fast", all, "|")
	n = 0
	for (i = 1; i <= nc; i++) if (all[i] in seen) cols[++n] = all[i]
	for (t = 1; t <= 2; t++) {
		print (t == 1 ? "Compile time (µs)" : "Run time (µs)")
		print ""
		line = "| Kernel |"; sep = "| --- |"
		for (i = 1; i <= n; i++) { line = line " " cols[i] " |"; sep = sep " --- |" }
		print line; print sep
		for (k = 1; k <= nk; k++) {
			line = "| `" ks[k] "` |"
			for (i = 1; i <= n; i++) {
				key = ks[k] SUBSEP cols[i]
				line = line " " fmt(t == 1 ? comp[key] : run[key]) " |"
			}
			print line
		}
		print ""
	}
}
