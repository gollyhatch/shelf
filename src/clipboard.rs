//! Writing text to the system clipboard.

use std::sync::Mutex;

use anyhow::{Result, anyhow};
use copypasta::{ClipboardContext, ClipboardProvider};

/// X11 keeps the selection in the process that set it, so the context lives
/// for the whole run instead of being dropped after each copy.
static CLIPBOARD: Mutex<Option<ClipboardContext>> = Mutex::new(None);

/// Copy `text` to the system clipboard.
pub fn write(text: &str) -> Result<()> {
    let mut clipboard =
        CLIPBOARD.lock().unwrap_or_else(|err| err.into_inner());
    if clipboard.is_none() {
        *clipboard = Some(
            ClipboardContext::new()
                .map_err(|err| anyhow!("clipboard: {err}"))?,
        );
    }
    clipboard
        .as_mut()
        .expect("clipboard initialized")
        .set_contents(text.to_owned())
        .map_err(|err| anyhow!("clipboard: {err}"))
}
