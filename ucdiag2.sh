#!/bin/bash
set -u
journalctl -u usercenter-api --no-pager -n 8 --output=cat | grep -iE "fatal|error|listening" | tail -3
grep -c "^USERCENTER_ENVIRONMENT=" /etc/usercenter/usercenter.env
grep "^USERCENTER_ENVIRONMENT=" /etc/usercenter/usercenter.env
