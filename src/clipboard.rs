//! Writing text to the system clipboard.
//!
//! Desktop clipboard ownership is tied to a running process, so shelf hands
//! the text to the platform's clipboard tool instead of holding it itself.

use std::io::{ErrorKind, Write};
use std::process::{Command, Stdio};

use anyhow::{Result, anyhow, bail};

/// Copy `text` with the first available platform tool.
pub fn write(text: &str) -> Result<()> {
    let candidates: &[(&str, &[&str])] = if cfg!(target_os = "macos") {
        &[("pbcopy", &[])]
    } else if cfg!(target_os = "windows") {
        &[("clip", &[])]
    } else {
        &[
            ("wl-copy", &[]),
            ("xclip", &["-selection", "clipboard"]),
            ("xsel", &["--clipboard", "--input"]),
        ]
    };

    let mut failure = None;
    for (program, args) in candidates {
        match run(program, args, text) {
            Ok(()) => return Ok(()),
            Err(Failure::Missing) => {}
            Err(Failure::Failed(err)) => failure = Some(err),
        }
    }
    match failure {
        Some(err) => Err(err),
        None => bail!(
            "no clipboard tool found (install wl-clipboard, xclip or xsel)"
        ),
    }
}

enum Failure {
    /// The tool is not installed; try the next candidate.
    Missing,
    Failed(anyhow::Error),
}

fn run(
    program: &str,
    args: &[&str],
    text: &str,
) -> std::result::Result<(), Failure> {
    let mut child = Command::new(program)
        .args(args)
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|err| {
            if err.kind() == ErrorKind::NotFound {
                Failure::Missing
            } else {
                Failure::Failed(anyhow!("{program}: {err}"))
            }
        })?;

    if let Some(mut stdin) = child.stdin.take()
        && let Err(err) = stdin.write_all(text.as_bytes())
    {
        let _ = child.wait();
        return Err(Failure::Failed(anyhow!("{program}: {err}")));
    }

    let status = child
        .wait()
        .map_err(|err| Failure::Failed(anyhow!("{program}: {err}")))?;
    if status.success() {
        Ok(())
    } else {
        Err(Failure::Failed(anyhow!("{program}: {status}")))
    }
}
