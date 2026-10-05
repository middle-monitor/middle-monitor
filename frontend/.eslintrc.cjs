// ESLint 8 flat config is not used here: the installed plugins (react-hooks,
// react-refresh) still ship eslintrc presets, and the build pins eslint 8.
module.exports = {
  root: true,
  env: { browser: true, es2022: true, node: true },
  extends: [
    'eslint:recommended',
    'plugin:@typescript-eslint/recommended',
    'plugin:react-hooks/recommended',
  ],
  parser: '@typescript-eslint/parser',
  parserOptions: { ecmaVersion: 'latest', sourceType: 'module' },
  plugins: ['react-refresh', '@typescript-eslint'],
  // dist/ and the prerender output are build artefacts; scratchpads are throwaway.
  ignorePatterns: ['dist', 'node_modules', 'scratchpad*.ts', 'test-results', 'playwright-report'],
  rules: {
    'react-refresh/only-export-components': 'off',
    // The TS compiler already fails the build on an unused variable, and tsc is
    // the one that knows about type-only imports. Keeping both only produces
    // duplicate reports, so the underscore escape hatch lives here alone.
    '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
    'no-unused-vars': 'off',
    // The codebase types caught errors as `any` and narrows them through
    // apiErrorMessage(). Enforcing `unknown` here would rewrite ~60 catch
    // blocks without making one of them safer, so the rule stays off rather
    // than accumulating inline disables.
    '@typescript-eslint/no-explicit-any': 'off',
  },
};
