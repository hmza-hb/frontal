const { defineConfig } = require("eslint/config");
const expoConfig = require("eslint-config-expo/flat");
const typescriptEslint = require("@typescript-eslint/eslint-plugin");
const globals = require("globals");
const noComments = require("./eslint/no-comments");

module.exports = defineConfig([
  expoConfig,
  {
    ignores: ["dist/**", "web-build/**", "node_modules/**"],
  },
  {
    plugins: {
      "@typescript-eslint": typescriptEslint,
      frontal: noComments,
    },
    rules: {
      "@typescript-eslint/no-explicit-any": "error",
      "frontal/no-comments": "error",
    },
  },
  {
    files: ["**/*.test.ts", "**/*.test.tsx"],
    languageOptions: {
      globals: globals.jest,
    },
  },
]);
