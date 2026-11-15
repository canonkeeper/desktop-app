#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# The pkexec of a runner in the smoke run of an update (specs/2026-11-13-desk-updates.md UB3.7): a runner has no agent of
# polkit to ask for the password of the administrator, so the job puts this one at /usr/bin/pkexec of its own machine —
# it runs the command as root at once, as pkexec does once the password is given. Never put on a computer of a person.
if [ "$(id -u)" = 0 ]; then
  exec "$@"
fi
exec sudo -n -- "$@"
