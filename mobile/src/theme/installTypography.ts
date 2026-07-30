import { Text, TextInput, type TextProps, type TextInputProps } from 'react-native';
import { fonts } from './tokens';

type Defaultable<Props> = {
  defaultProps?: Props;
};

/**
 * React Native has no document-level font cascade. Set the regular brand face
 * once so every unstyled Text and TextInput uses the same bundled family;
 * semantic type tokens still select the exact medium, semibold, bold, or mono
 * files where those roles are intentional.
 */
const defaultTextStyle = { fontFamily: fonts.sansRegular };
const text = Text as unknown as Defaultable<TextProps>;
const textInput = TextInput as unknown as Defaultable<TextInputProps>;

text.defaultProps = {
  ...text.defaultProps,
  style: [defaultTextStyle, text.defaultProps?.style],
};
textInput.defaultProps = {
  ...textInput.defaultProps,
  style: [defaultTextStyle, textInput.defaultProps?.style],
};
