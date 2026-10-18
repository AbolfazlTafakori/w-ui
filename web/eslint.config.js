// What the panel's frontend is checked against: the language's own
// recommended rules and Vue's essential ones -- the mistakes, not a style.
import js from '@eslint/js'
import vue from 'eslint-plugin-vue'
import globals from 'globals'

export default [
  { ignores: ['node_modules/**', '../internal/web/dist/**'] },
  js.configs.recommended,
  ...vue.configs['flat/essential'],
  {
    files: ['**/*.{js,vue}'],
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals: { ...globals.browser },
    },
    rules: {
      // Icon, Toggle and Sparkline are named for what they are; the rule is
      // about clashing with HTML elements, which none of them do.
      'vue/multi-word-component-names': 'off',
    },
  },
  {
    files: ['**/*.test.js', 'vite.config.js', 'eslint.config.js'],
    languageOptions: { globals: { ...globals.node } },
  },
]
