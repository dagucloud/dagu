const HtmlWebpackPlugin = require('html-webpack-plugin');
const { merge } = require('webpack-merge');
const common = require('./webpack.common.js');
const path = require('path');
const CopyWebpackPlugin = require('copy-webpack-plugin');

module.exports = merge(common, {
  mode: 'development',
  devtool: 'eval-source-map',
  profile: true,
  devServer: {
    historyApiFallback: { disableDotRule: true },
    port: 8081,
    proxy: [
      {
        context: [
          '/api',
          '/assets/dag.schema.json',
          '/assets/config.schema.json',
        ],
        target: 'http://localhost:8080',
        changeOrigin: true,
        ws: true,
      },
    ],
    client: {
      overlay: {
        runtimeErrors: (error) => {
          // Ignore ResizeObserver errors - they're benign and common with Monaco Editor
          if (error.message?.includes('ResizeObserver loop')) {
            return false;
          }
          return true;
        },
      },
    },
  },
  module: {
    rules: [
      {
        test: /\.tsx?$/,
        use: [
          {
            loader: 'esbuild-loader',
            options: {
              loader: 'tsx',
              target: 'es2015',
            },
          },
        ],
        include: path.resolve(__dirname, 'src'),
        exclude: path.resolve(__dirname, 'node_modules'),
      },
    ],
  },
  plugins: [
    new HtmlWebpackPlugin({
      template: 'index.html',
    }),
    new CopyWebpackPlugin({
      patterns: [
        {
          from: 'favicon.ico',
          to: 'assets/favicon.ico',
        },
      ],
    }),
  ],
  optimization: {
    removeAvailableModules: false,
    removeEmptyChunks: false,
    splitChunks: false,
  },
  output: {
    filename: 'bundle.js',
    pathinfo: false,
    path: path.resolve(__dirname, 'dist'),
    publicPath: '/',
    clean: true,
  },
});
