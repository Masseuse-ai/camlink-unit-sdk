# Security

## Reporting

Report vulnerabilities privately through GitHub's
[security advisory form](https://github.com/Masseuse-ai/camlink-unit-sdk/security/advisories/new)
for this repository. Do not open a public issue for a vulnerability. Reports
are acknowledged within three business days.

## What this module is

A library: the interfaces and transports a unit driver helper for
masseuse-camlink is built from, and nothing that runs by itself. It opens
no network connection of its own. Its Bluetooth and serial packages reach
the devices a helper is written for, on the computer the helper runs on,
through the operating system's own stacks (CoreBluetooth, BlueZ, the
Windows Runtime, the serial drivers); the helper that uses them is handed
its device's link by the connector and nothing else, as the connector's
`docs/UNITS.md` sets out.

## Supported versions

The latest minor version of the current major version receives fixes.
