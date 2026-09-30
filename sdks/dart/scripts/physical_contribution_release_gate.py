#!/usr/bin/env python3
"""Require same-source signed Android/iPhone contribution-delete evidence."""

import argparse
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / 'sdks/dart/offline/tool'))
from physical_receipt_attestation import (  # noqa: E402
    CONTRIBUTION_TARGET, validate_archived_receipt_evidence,
)

EVIDENCE = 'sdks/dart/example/evidence/contribution-delete'
EXPECTED = {f'{EVIDENCE}/{platform}{suffix}.json'
            for platform in ('android', 'ios') for suffix in ('', '-marker')}
SCENARIOS = frozenset({
    'authenticated_https', 'invalid_contribution_identity',
    'indexed_duplicate_missing_expired', 'selective_delete_preserves_put_base',
    'identity_cdc_edge_refetch', 'unsupported_offline_family',
    'dispatch_marker_before_send', 'committed_response_loss',
    'physical_sigkill_status_first', 'original_true_false_replay',
    'no_mutation_resend_after_restart',
})


def git(*args, root=ROOT):
    return subprocess.check_output(['git', '-C', str(root), *args], text=True).strip()


def source_identity(root=ROOT):
    if git('status', '--porcelain', root=root):
        raise ValueError('physical release checkout must be clean')
    head = git('rev-parse', 'HEAD', root=root)
    parents = git('show', '-s', '--format=%P', 'HEAD', root=root).split()
    if len(parents) != 1:
        raise ValueError('physical release must be one evidence-only child')
    source = parents[0]
    changed = set(git('diff', '--name-only', source, head, root=root).splitlines())
    if changed != EXPECTED:
        raise ValueError('release child must change exactly the four physical records')
    return source, head


def validate_release(root=ROOT, now=None):
    source, head = source_identity(root)
    checked_at = now or datetime.now(timezone.utc)
    run_ids = set()
    for platform in ('android', 'ios'):
        marker = Path(root, EVIDENCE, f'{platform}-marker.json')
        record = Path(root, EVIDENCE, f'{platform}.json')
        validate_archived_receipt_evidence(marker, record, tested_commit=source,
            platform=platform, required_scenarios=SCENARIOS,
            target=CONTRIBUTION_TARGET, now=checked_at)
        values = json.loads(record.read_text())
        if values['runId'] in run_ids:
            raise ValueError('physical platform run IDs must differ')
        run_ids.add(values['runId'])
        recorded = datetime.fromisoformat(values['recordedAt'].replace('Z', '+00:00'))
        if recorded < checked_at - timedelta(days=30):
            raise ValueError('physical evidence exceeds private-custody retention')
    return source, head


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--tag', required=True)
    args = parser.parse_args()
    if not re.fullmatch(r'sdks/dart/v[0-9]+\.[0-9]+\.[0-9]+', args.tag):
        parser.error('an independent parent Dart release tag is required')
    source, head = validate_release()
    if git('rev-parse', f'refs/tags/{args.tag}^{{commit}}') != head:
        raise ValueError('release tag does not identify the physical evidence child')
    print(f'Physical contribution Delete qualified: source={source} tag={args.tag}')


if __name__ == '__main__':
    main()
