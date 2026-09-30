# camlink-unit-sdk

What a unit driver helper for [masseuse-camlink](https://github.com/Masseuse-ai/masseuse-camlink)
is built from. The connector serves stimulation units through drivers; a
driver for a unit the connector's own tree does not carry is a *helper*, a
separate program the connector finds in its units directory, runs as a
child process and speaks to over its standard input and output (the
connector's `docs/UNITS.md`). This module is the helper's side of that
arrangement and the packages a driver needs to reach its device:

| package | what |
|---|---|
| `unit` | the driver interfaces (`Driver`, `Finder`, `Lister`, `Selector`), the types both ends exchange (`Capabilities`, `Status`, `Frame`, `Command`, `Result`, `Unit`, `Identity`), the connector's caps and settings, and the actuator model (`Actuator`, `Sensor`, `Actuate`) for a device that is more than an intensity channel |
| `helper` | `Main` and `Serve`: a helper program in one call; the wire types of the protocol, which the connector's Host uses too |
| `ble` | a Bluetooth Low Energy central in pure Go (CoreBluetooth through the Objective-C runtime on macOS, BlueZ over D-Bus on Linux, the Windows Runtime on Windows): scan, connect, write, read, subscribe, MTU; and an `Advertiser` for the connectionless device that listens for manufacturer data (Linux and Windows) |
| `serialport` | a serial port and the enumeration of USB serial adapters |

Everything here builds with `CGO_ENABLED=0` for darwin, linux and windows.

## A helper in one file

```go
package main

import (
	"log/slog"

	"github.com/Masseuse-ai/camlink-unit-sdk/helper"
	"github.com/Masseuse-ai/camlink-unit-sdk/unit"
	"example.com/family" // the family's driver: a unit.Finder over ble or serialport
)

const Name = "family" // the program is camlink-unit-family

func main() {
	helper.Main(func(stateDir string, log *slog.Logger) (unit.Finder, helper.Options, error) {
		return family.NewFinder("", log), helper.Options{Name: Name, Kinds: []unit.Kind{family.Kind}, Log: log}, nil
	})
}
```

The connector runs it as `camlink-unit-family --state-dir <dir> --log-level <level>`,
greets it with `hello`, asks it to `list`, `select` and `find` units, and
drives the unit it opened with `arm`, `execute`, `status`, `telemetry`,
`release` and `close`. `Serve` answers all of that from the `unit.Finder`
and the `unit.Driver` it returns; the family writes the two.

## The driver

A family implements `unit.Finder` (find and describe the units in reach;
`unit.Lister` and `unit.Selector` when it can list them and be pinned to
one) and `unit.Driver` for an open unit. The contract that matters most is
`Release`: every output to zero and stopped, whatever the state, carrying
on through failures. The connector calls it when a session ends, when the
phone asks, and before it gives up on a link.

A device that is an intensity channel or two on a program describes itself
through `Capabilities` alone: `Channels` names the channels the connector
may drive (`"a"`, or `"a"` and `"b"`), a level command names its channel
(`Command.Channel`, A when it names none) and its result says which one it
moved (`Result.Channel`); a command for a channel the device does not list
is refused by `CheckCaps` before the driver sees it. A device with more, a second motor, a
rotation, a heater, a light, a piston that takes a position, also
implements `unit.Actuating` (and `unit.Sensing` for its inputs): it lists
its `Actuators` once and takes `Actuate` commands against them, each
checked by `unit.CheckActuate` against the list before the driver sees it.
The library a family publishes exposes everything the device can do; the
connector drives the part it understands.

## Discovery over Bluetooth

`ble.Open` gives a `Central`. `Scan` reports every advertisement to a match
function (a name, a name prefix, an advertised service, manufacturer data),
`Connect` opens the one chosen and discovers its services, and the `Conn`
writes, reads and subscribes by service and characteristic UUID.
`Characteristics` lists what was discovered under a service with what each
allows (`ble.Find` picks the first that writes, or notifies), for a vendor
service whose layout the driver does not know in advance. `MTU` says how
much one write carries when the system knows.

A device that never connects and only listens for advertisements is driven
with `ble.OpenAdvertiser`: each command is a new `Broadcast` of
manufacturer data. Linux (BlueZ) and Windows transmit it; macOS reports
`ErrUnsupported`, CoreBluetooth's peripheral role carrying no manufacturer
data.

A driver's tests use `ble/bletest`: a `Peripheral` declares the
characteristics the real unit presents, records every write, answers reads
and pushes notifications, and a `Central` over a few of them stands in for
the system's, so a family's fake unit is a peripheral with an `OnWrite`
that keeps the unit's state.

## The wire, version 2

Version 1 is the protocol the connector's `docs/UNITS.md` describes.
Version 2 adds the actuator model without changing anything of version 1:
`find` answers with `actuators` and `sensors` when the driver has them,
`actuate` sends one `unit.Actuate` (answered as an empty result or an
error), and `readings` returns the sensors' values. A connector of
version 1 ignores the additions; a helper of version 1 answers the new
methods with the `unsupported` code. Errors the helper raises on its own
account (`no_driver`, `unsupported`) carry their code across as
`helper.CodedError`.

## Versions

Tags are `vX.Y.Z`. Within a major version the exported API only grows
(v0.2.0 added `Conn.Characteristics`, which a `ble.Conn` implementation
of v0.1.0 must add; v0.3.0 added `ble/bletest`);
the connector pins the version it builds with, and a helper pins the one
it was written against.

## Verifying

Every tag is built and tested by the workflow in `.github/workflows/ci.yml`
on the three systems; the module is fetched by its import path through
the Go module proxy, which records each version's checksum in the
checksum database. There is no binary to download.
