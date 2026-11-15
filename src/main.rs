//! Command shelf browses and edits the metadata of EPUB files. Run it without
//! arguments for the interactive browser, or see "shelf help" for the batch
//! commands.

fn main() {
    std::process::exit(shelf::cli::run());
}
