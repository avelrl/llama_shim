i=1
while [ "$i" -le 200 ]; do
  printf 'line-%03d codex-eval-long-stdout\n' "$i"
  i=$((i + 1))
done
printf 'LONG_STDOUT_DONE\n'
