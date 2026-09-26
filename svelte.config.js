import adapter from '@sveltejs/adapter-static';
export default {
  kit: {
    adapter: adapter({ pages: 'build/assets', assets: 'build/assets', fallback: 'index.html' })
  }
};
