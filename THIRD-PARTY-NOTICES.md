# Third-party notices

net-merge is MIT licensed (see `LICENSE`). It links the following libraries, all under permissive
licenses (license texts are in each project's repository and in the Go module cache):

| Component | License | Use |
|---|---|---|
| [tun2socks](https://github.com/xjasonlyu/tun2socks) | MIT | TUN device + userland TCP/IP stack glue |
| [gVisor](https://github.com/google/gvisor) (netstack) | Apache-2.0 | userland TCP/IP stack |
| [wireguard-go](https://git.zx2c4.com/wireguard-go) / [wintun Go bindings](https://git.zx2c4.com/wintun-go) | MIT | Windows TUN device access |
| [fyne.io/systray](https://github.com/fyne-io/systray) | Apache-2.0 | system tray icon |
| [vishvananda/netlink](https://github.com/vishvananda/netlink) | Apache-2.0 | Linux TUN addresses and routes, default-route discovery |
| golang.org/x/sys, golang.org/x/net, golang.org/x/time, golang.org/x/crypto | BSD-3-Clause | Windows and Linux system APIs, support code |
| Other transitive modules (see `go.mod` / `go.sum`) | MIT / Apache-2.0 / BSD | |

## Wintun

The installer and portable zip include `wintun.dll`, the prebuilt, signed driver from
<https://www.wintun.net/>, unmodified, under the Prebuilt Binaries License. Copyright (C) 2018-2021
WireGuard LLC. The license text is shipped next to it as `wintun-LICENSE.txt`. The DLL is not stored
in this repository: `scripts/fetch-wintun.ps1` downloads it and verifies its SHA-256 and its
WireGuard LLC code signature.

## Windows flyout SVG rendering

SVG ISP favicons are rendered with [oksvg](https://github.com/srwiley/oksvg) and [rasterx](https://github.com/srwiley/rasterx), both BSD-3-Clause.

BSD 3-Clause License

Copyright (c) 2018, Steven R Wiley
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

* Redistributions of source code must retain the above copyright notice, this
  list of conditions and the following disclaimer.

* Redistributions in binary form must reproduce the above copyright notice,
  this list of conditions and the following disclaimer in the documentation
  and/or other materials provided with the distribution.

* Neither the name of the copyright holder nor the names of its
  contributors may be used to endorse or promote products derived from
  this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
