"""Regression checks for the phone window geometry and host mount specification."""
import json
import unittest
from types import SimpleNamespace
from unittest.mock import Mock

from Xlib import Xutil
import control
from view import View, dimensions, fitted_window


class PhoneWindowTests(unittest.TestCase):
    def test_viewport_matches_electron_dpi_even_while_typing(self):
        for size, expected in [((390, 672), (585, 1008)), ((672, 390), (1008, 585)),
                               ((390, 242), (585, 363))]:
            self.assertEqual(dimensions(*size), expected)

    def test_only_verified_maximized_window_relaxes_desktop_minimum(self):
        # A mobile display of 585px starts with the actual Claude Electron
        # minimum 900x600, which Openbox honors until this hint is lowered.
        from view import Failure
        view = View.__new__(View)
        view.size = (390, 672)
        view.root = Mock()
        view.conn = Mock()
        view.conn.intern_atom.side_effect = lambda name: name
        view.values = lambda w, k: [12] if w is view.root else [
            '_NET_WM_STATE_MAXIMIZED_VERT', '_NET_WM_STATE_MAXIMIZED_HORZ']
        real = Mock()
        real.get_geometry.return_value = SimpleNamespace(width=900, height=1008)
        hints = dict(flags=Xutil.PMinSize, min_width=900, min_height=600, win_gravity=10)
        real.get_wm_normal_hints.return_value = SimpleNamespace(**hints, _data=hints)
        real.get_wm_transient_for.return_value = None
        view.window = lambda _: real
        view.role = lambda _: ''
        with self.assertRaises(Failure):
            view.active()
        real.set_wm_normal_hints.assert_not_called()
        view.role = lambda _: 'desktop'
        with self.assertRaises(Failure):
            view.active()
        real.set_wm_normal_hints.assert_called_once_with(**{**hints, 'min_width': 585})

    def test_nonmaximized_dialog_uses_matching_picture_and_input_geometry(self):
        ratio, left, top = fitted_window((390, 672), SimpleNamespace(width=600, height=300))
        self.assertEqual((150 * ratio + left, 150 * ratio + top), (97.5, 336))

    def test_existing_project_mounts_use_literal_guest_paths_in_transient_unit(self):
        mounts = ('25 11 0:88 / /opt/QA\\040%h rw - fuse.sshfs :/qa rw\n'
                  '27 11 0:89 / /Users/A\\134B rw - virtiofs data rw\n'
                  '29 11 0:90 / /home rw - ext4 /dev/vda1 rw\n')
        shares = control.host_shares(mounts)
        self.assertEqual(shares, ['/Users/A\\B', '/opt/QA %h'])
        self.assertEqual(json.loads(control.unit_path(shares[1])), '-/opt/QA %h')


if __name__ == '__main__':
    unittest.main()
