# ucmix

Unofficial Go library and CLI for controlling PreSonus StudioLive Series III
mixers over the **UCNET** network protocol.

> **Unofficial.** ucmix is not affiliated with, authorized, or endorsed by
> PreSonus Audio Electronics, Inc. "PreSonus", "StudioLive", "UC Surface", and
> "UCNET" are trademarks of their respective owners. This project communicates
> with the mixer's network protocol for interoperability only.

## What it does

Read and write a StudioLive mixer's state programmatically — channel names,
input patch, 48V, HPF, monitor mixes, sends, limiters, reverb, scene recall and
store. The headline feature is **board as code**: `verify` and `apply` a full
mixer configuration from a declarative file instead of tapping it into UC
Surface by hand.

## Status

Early construction. The UCNET protocol has been reverse-engineered and proven
against a real StudioLive 32R; the Go library and CLI are being built.

## License

MIT — see [LICENSE](LICENSE).
