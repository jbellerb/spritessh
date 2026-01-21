# spritessh

An SSH bridge for all your [Sprites](https://sprites.dev).

> [!WARNING]
> This was built as a demonstration of the Sprites SDK. This is not production
> software. I cannot guarantee any of this will work for you.

## Features

- Transparent proxying of shell sessions with your Sprites
- No Sprite-side setup required
- Connect with multiple Sprites at the same time (Sprite concurrency limits
  still apply)

## Getting Started

```sh
# Install spritessh
go install github.com/jbellerb/spritessh@latest

# Alternatively, clone the repo and run
# make build

# Start the server on the configured port
spritessh serve -l ':2222'
```

In another terminal:

```sh
ssh $sprite_name@localhost -p 2222
```

For convenience, consider adding an alias to your `~/.ssh/config`:

```
Host sprites
	HostName 127.0.0.1
	Port 2222
```

<br />

#### License

<sup>
Copyright (C) jae beller, 2026.
</sup>
<br />
<sup>
Released under the MIT License. See <a href="LICENSE">LICENSE</a> for more information.
</sup>
