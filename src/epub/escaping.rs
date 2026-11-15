//! XML escaping shared by the package writer.

/// Escape a value for use in XML text or an attribute value.
pub fn escape_xml(value: &str) -> String {
    quick_xml::escape::escape(value).into_owned()
}
