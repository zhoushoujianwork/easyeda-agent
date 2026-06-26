#!/usr/bin/env python3
"""Single source of truth for netflag/netport body orientation.

The whole 12-entry rotation table is determined by four facts (see
orientation.json): the +90° body cycle ``up → left → down → right`` and the
body direction at rotation 0 for each family (power=up, ground=down, port=right).
``derive`` reconstructs the table; ``load_body_rotation`` reads the canonical
spec next to this file. The lint check and the connector's connect_pin both
derive from these same facts, so they can never drift — tests/run.py asserts it.
"""
import json
import os

# orientation.json is the canonical truth and lives in the easyeda-conventions
# skill (single source); this operational script reads it across the skill boundary.
DEFAULT_SPEC = os.path.join(
    os.path.dirname(os.path.abspath(__file__)),
    '..', '..', 'easyeda-conventions', 'references', 'orientation.json')


def derive(rotation_cycle, body_anchor):
    """Derive {family: {direction: rotation}} from the cycle + per-family anchor.

    rotation that makes the body point `direction` = (index(direction) -
    index(anchor)) mod 4, times 90. Pure function — no I/O.
    """
    table = {}
    for family, anchor in body_anchor.items():
        ai = rotation_cycle.index(anchor)
        table[family] = {
            d: ((rotation_cycle.index(d) - ai) % 4) * 90
            for d in rotation_cycle
        }
    return table


def load_spec(path=DEFAULT_SPEC):
    with open(path) as f:
        return json.load(f)


def load_body_rotation(path=DEFAULT_SPEC):
    """Return the derived body-rotation table from the canonical spec."""
    spec = load_spec(path)
    return derive(spec['rotationCycle'], spec['bodyAnchorAtRot0'])


if __name__ == '__main__':
    # Print the derived table so a human can eyeball it against frozenTable.
    print(json.dumps(load_body_rotation(), indent=2))
