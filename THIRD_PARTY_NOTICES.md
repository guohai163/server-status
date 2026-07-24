# Third-Party Notices

## smartmontools

Windows SMART collection uses the unmodified smartmontools 7.5 Windows distribution from the
[official smartmontools release](https://github.com/smartmontools/smartmontools/releases/tag/RELEASE_7_5).
smartmontools is distributed under the GNU General Public License, version 2 or later.

Server Status release artifacts include:

- `server-status-smartctl-windows-setup.exe`: the unmodified official Windows installer.
- `server-status-smartctl-source.tar.gz`: the corresponding unmodified source archive.

The installed smartmontools documentation includes its copyright notices, warranty disclaimer,
and `COPYING.txt`. Server Status invokes `smartctl.exe` as a separate process and parses its JSON
output; smartmontools is not linked into the Server Status Agent.
