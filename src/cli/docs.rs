//! `completion` and `man`: generated shell and manual documentation.

use std::io::Write;
use std::path::PathBuf;

use anyhow::{Context, Result, bail};
use clap::{Args, CommandFactory};
use clap_complete::{Shell, generate};
use clap_mangen::Man;

use super::Cli;

#[derive(Args)]
pub struct CompletionArgs {
    /// shell to generate for: bash, zsh, fish or powershell
    #[arg(value_name = "SHELL")]
    pub shell: String,
}

#[derive(Args)]
pub struct ManArgs {
    /// write all man pages into this directory
    #[arg(long, value_name = "DIR")]
    pub dir: Option<PathBuf>,
}

pub fn completion(args: CompletionArgs) -> Result<i32> {
    let shell = match args.shell.as_str() {
        "bash" => Shell::Bash,
        "zsh" => Shell::Zsh,
        "fish" => Shell::Fish,
        "powershell" => Shell::PowerShell,
        other => bail!(
            "unsupported shell {other:?} (want bash, zsh, fish or powershell)"
        ),
    };
    let mut cmd = Cli::command();
    let mut stdout = std::io::stdout();
    generate(shell, &mut cmd, "shelf", &mut stdout);
    Ok(0)
}

pub fn man(args: ManArgs) -> Result<i32> {
    let mut buf = Vec::new();
    render_page(&Cli::command(), &mut buf)?;
    match args.dir {
        None => {
            let mut stdout = std::io::stdout();
            stdout.write_all(&buf)?;
        }
        Some(dir) => {
            std::fs::create_dir_all(&dir)
                .with_context(|| format!("create {}", dir.display()))?;
            write_tree(&Cli::command(), "shelf", &dir)?;
        }
    }
    Ok(0)
}

fn render_page(cmd: &clap::Command, out: &mut Vec<u8>) -> Result<()> {
    Man::new(cmd.clone())
        .title("SHELF")
        .section("1")
        .source("shelf")
        .manual("shelf manual")
        .render(out)
        .context("render man page")?;
    Ok(())
}

fn write_tree(
    cmd: &clap::Command,
    page_name: &str,
    dir: &std::path::Path,
) -> Result<()> {
    let page = Man::new(cmd.clone())
        .title("SHELF")
        .section("1")
        .source("shelf")
        .manual("shelf manual");
    let path = dir.join(format!("{page_name}.1"));
    let mut buf = Vec::new();
    page.render(&mut buf)?;
    std::fs::write(&path, buf)
        .with_context(|| format!("write {}", path.display()))?;
    for sub in cmd.get_subcommands() {
        if sub.get_name() == "help" {
            continue;
        }
        let child = format!("{page_name}-{}", sub.get_name());
        write_tree(sub, &child, dir)?;
    }
    Ok(())
}
