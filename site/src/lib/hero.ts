/**
 * The hero photograph and its licence obligations.
 *
 * One image ships. It backs the home hero, every page header band and the
 * closing CTA panel, and its credit is rendered in the footer - so the file,
 * the treatment nudges it needs, and the attribution all live here together
 * rather than being repeated at each use site.
 *
 * The image is served in three width tiers, selected by media query in
 * global.css (`--img-trinity`): a CSS background has no `srcset`, and
 * `image-set()` keys off device pixel ratio rather than viewport width.
 */

/**
 * Attribution for a Creative Commons photograph, in the four parts the licences
 * ask for - Title, Author, Source, Licence - plus a note of the changes made,
 * which CC BY and CC BY-SA both require you to indicate.
 *
 * Verify every field against the file's own metadata at the source, not a
 * third-party search index: two of the authors considered for this slot were
 * wrong when taken from an index, and only came right when read from Wikimedia
 * Commons itself.
 *
 * This is REQUIRED, deliberately. Making it optional would let a replacement
 * image ship with the credit silently absent, which for a share-alike photo is
 * a licence breach rather than a cosmetic omission.
 */
export interface HeroAttribution {
  /** The work's title as recorded at the source. */
  title: string
  author: string
  /** The source page, not the file: where a reader can verify the licence. */
  sourceUrl: string
  licence: string
  licenceUrl: string
  /** What we did to it. "Indicate if changes were made" is a licence term. */
  changes: string
}

export interface HeroImage {
  /** The middle width tier; the others are derived in global.css. */
  file: string
  attribution: HeroAttribution
  /** Post-desaturation brightness multiplier applied to the photo layer. */
  brightness: number
  /** background-position for the photo layer. */
  position: string
}

export const HERO: HeroImage = {
  file: '/img/hero/trinity-1920.webp',
  attribution: {
    title: 'Long Room Interior, Trinity College Dublin, Ireland',
    author: 'Diliff',
    sourceUrl:
      'https://commons.wikimedia.org/wiki/File:Long_Room_Interior,_Trinity_College_Dublin,_Ireland_-_Diliff.jpg',
    licence: 'CC BY-SA 4.0',
    licenceUrl: 'https://creativecommons.org/licenses/by-sa/4.0',
    changes: 'cropped and converted to greyscale',
  },
  brightness: 0.45,
  position: '50% 40%',
}

/**
 * True when the licence is share-alike, so our modified version carries the
 * same obligation and the footer must say so. Derived rather than written down,
 * so swapping in a CC0 image drops that sentence without anyone remembering to.
 */
export const HERO_IS_SHARE_ALIKE = HERO.attribution.licence.includes('BY-SA')
