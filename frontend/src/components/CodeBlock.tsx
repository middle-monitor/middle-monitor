import { Highlight, themes, type PrismTheme } from 'prism-react-renderer';

interface CodeBlockProps {
  code: string;
  language: string;
}

// Same as the default vsDark theme (keeps its keyword/string/number colors),
// minus the per-line background color it normally sets — that background is
// a hair lighter than .doc-code-block's own background, so every line reads
// as a mismatched box instead of one flat code block.
const theme: PrismTheme = {
  ...themes.vsDark,
  plain: { ...themes.vsDark.plain, backgroundColor: undefined },
};

// Syntax-highlighted code sample for the documentation. Keeps the existing
// <pre><code> structure so .doc-code-block styling (background, padding,
// font) still applies around it.
export function CodeBlock({ code, language }: CodeBlockProps) {
  return (
    <Highlight code={code.replace(/\n$/, '')} language={language} theme={theme}>
      {({ tokens, getLineProps, getTokenProps }) => (
        <pre>
          <code>
            {tokens.map((line, i) => (
              <span key={i} {...getLineProps({ line })}>
                {line.map((token, key) => (
                  <span key={key} {...getTokenProps({ token })} />
                ))}
                {'\n'}
              </span>
            ))}
          </code>
        </pre>
      )}
    </Highlight>
  );
}
