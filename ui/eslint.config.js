import globals from 'globals';
export default [{files:['src/**/*.{js,jsx,mjs}'],languageOptions:{ecmaVersion:'latest',sourceType:'module',globals:{...globals.browser,...globals.node},parserOptions:{ecmaFeatures:{jsx:true}}},rules:{'no-undef':'error','no-unreachable':'error','valid-typeof':'error'}}];
