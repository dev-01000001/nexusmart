// Next drops its default PostCSS plugins once this file exists. Re-add them after Tailwind so legacy CSS compiles
// exactly as before (options mirror getDefaultPlugins in next/dist/build/webpack/config/blocks/css/plugins.js).
// Remove the two Next plugins in phase 5 together with the CSS Modules.
const config = {
  plugins: {
    "@tailwindcss/postcss": {},
    "next/dist/compiled/postcss-flexbugs-fixes": {},
    "next/dist/compiled/postcss-preset-env": {
      browsers: ["chrome 64", "edge 79", "firefox 67", "opera 51", "safari 12"],
      autoprefixer: { flexbox: "no-2009" },
      stage: 3,
      features: { "custom-properties": false },
    },
  },
};

export default config;
