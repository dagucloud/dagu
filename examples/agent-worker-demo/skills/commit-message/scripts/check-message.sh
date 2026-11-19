#!/bin/sh
# Check the subject line of a commit message against references/format.md.
# usage: check-message.sh "<subject line>"
# The script only reads its argument. It does not run git and writes no file.
set -eu

subject=${1:?usage: check-message.sh "<subject line>"}

if [ "${#subject}" -gt 72 ]; then
  echo "the subject line has more than 72 characters" >&2
  exit 1
fi
case $subject in
  fix\(*\):\ * | feat\(*\):\ * | test\(*\):\ * | docs\(*\):\ * | chore\(*\):\ * | \
  fix:\ * | feat:\ * | test:\ * | docs:\ * | chore:\ *) ;;
  *) echo "the subject line must start with <type>(<scope>): " >&2; exit 1 ;;
esac
case $subject in
  *.) echo "the summary must not end with a full stop" >&2; exit 1 ;;
esac
echo "ok"
