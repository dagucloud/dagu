const { merge } = require('webpack-merge');
const common = require('./webpack.common.js');
const path = require('path');
const { EsbuildPlugin } = require('esbuild-loader');
const HtmlWebpackPlugin = require('html-webpack-plugin');
const CopyWebpackPlugin = require('copy-webpack-plugin');

module.exports = merge(common, {
  mode: 'production',
  module: {
    rules: [
      {
        test: /\.tsx?$/,
        loader: 'esbuild-loader',
        options: {
          loader: 'tsx',
          target: 'es2015',
        },
        include: path.resolve(__dirname, 'src'),
        exclude: [path.resolve(__dirname, 'node_modules')],
      },
    ],
  },
  optimization: {
    minimize: true,
    minimizer: [
      new EsbuildPlugin({
        target: 'es2015',
        css: true,
      }),
    ],
  },
  plugins: [
    new HtmlWebpackPlugin({
      template: 'index.html',
      favicon: 'favicon.ico',
    }),
    new CopyWebpackPlugin({
      patterns: [
        {
          from: 'favicon.ico',
          to: '',
        },
        {
          from: 'favicon.ico',
          to: 'favicon.ico',
        },
      ],
    }),
  ],
  output: {
    filename: 'bundle.js',
    chunkFilename: '[name].[contenthash:16].bundle.js',
    path: path.resolve(__dirname, 'dist'),
    publicPath: 'auto',
    clean: true,
  },
});
